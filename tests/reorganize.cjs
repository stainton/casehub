// 「用例重整」（功能 AM，临时）的浏览器回归：未完成的测试版本挡住重整；探索归类 → 预览 → 应用后主线按 产品/模块 重建、
// 用例改编号、空出来的版本目录被删掉。脚本自带一个假的 planner 服务（真实的 HTTP 形状，不调用模型），启动 CaseHub 时用
// CASEHUB_PLANNER_URL 指向它。只对临时 CASEHUB_STORE=memory 服务运行。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const http=require('node:http');
const {randomUUID}=require('node:crypto');

const PLANNER_PORT=Number(process.env.CASEHUB_FAKE_PLANNER_PORT||4598);
const submissions=[];
function fakePlanner(){
  const jobs=new Map();
  const json=(res,status,body)=>{res.writeHead(status,{'Content-Type':'application/json','Access-Control-Allow-Origin':'*'});res.end(JSON.stringify(body))};
  const read=req=>new Promise(resolve=>{let body='';req.on('data',c=>body+=c);req.on('end',()=>resolve(JSON.parse(body)))});
  return http.createServer(async(req,res)=>{
    const path=new URL(req.url,'http://fake').pathname,[,,,,id,resource]=path.split('/'); // 派发时带 ?queue=false
    if(req.method==='POST'&&path==='/v1/planner/jobs'){
      const input=await read(req);
      submissions.push(input);
      const jobId=randomUUID(),at=new Date().toISOString();
      // 登录相关的进「登录」，其余进「资产区 / 同步」：同一功能来自 V1、V2 两个版本目录的用例会落到同一个目录。
      const assignments=input.organize.cases.map(c=>/登录/.test(c.title)
        ?{caseId:c.id,product:'AIGC',moduleCode:'LOGIN',folders:['登录'],category:'FUNC'}
        :{caseId:c.id,product:'AIGC',moduleCode:'ASSET',folders:['资产区','同步'],category:'FUNC'});
      const entry={job:{id:jobId,kind:'planner',status:'succeeded',stage:'completed',createdAt:at,updatedAt:at,finishedAt:at,lastEventId:1},
        result:{mode:'organize',products:input.organize.products.map(({code,name})=>({code,name})),modules:[],assignments,unassigned:[],explorationNotes:'',limitations:[{risk:'low',summary:'未进入回收站页面'}]}};
      jobs.set(jobId,entry);
      res.setHeader('Location',`/v1/planner/jobs/${jobId}`);
      return json(res,202,{...entry.job,status:'queued',stage:'queued'});
    }
    const entry=jobs.get(id);
    if(!entry)return json(res,404,{error:{code:'JOB_NOT_FOUND',message:'not found'}});
    if(resource==='result')return json(res,200,entry.result);
    if(resource==='release')return json(res,200,{});
    if(resource==='events'){
      res.writeHead(200,{'Content-Type':'text/event-stream','Access-Control-Allow-Origin':'*'});
      res.write(`event: snapshot\ndata: ${JSON.stringify(entry.job)}\n\n`);
      res.end();
      return;
    }
    json(res,200,entry.job);
  });
}

(async()=>{
  const planner=fakePlanner();
  await new Promise(resolve=>planner.listen(PLANNER_PORT,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
  try{
    const page=await browser.newPage();const errors=[];
    page.on('pageerror',e=>errors.push(e.stack||e.message));
    page.on('dialog',d=>d.accept());
    await page.goto(process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081');
    await page.waitForFunction(()=>typeof state!=='undefined'&&state&&document.querySelector('[data-reorganize-main]'));
    const stamp=Date.now().toString(36);
    // 两个测试版本各在一个以版本命名的目录里写了同一功能的用例，并合并回主线——这就是要重整的结构。
    const names=await page.evaluate(async stamp=>{
      const out=[];
      for(const [name,title] of [[`V1-${stamp}`,`资产菜单同步 ${stamp}`],[`V2-${stamp}`,`批量同步 ${stamp}`]]){
        await act('createVersion',{Name:name,Blank:true});
        const v=state.versions.find(x=>x.name===name).id;
        await act('createFolder',{VersionID:v,ParentID:'root',Name:name});
        await act('createCase',{VersionID:v,FolderID:state.folders.find(f=>f.VersionID===v&&f.Name===name).ID,Title:title});
        await act('merge',{VersionID:v});
        out.push(name);
      }
      return out;
    },stamp);

    // 版本还开着：只给出说明，不出表单。
    await page.locator('[data-reorganize-main]').click();
    await page.locator('#reorg-body').getByText(names[0]).waitFor();
    assert.equal(await page.locator('#reorg-form').count(),0);
    await page.locator('#reorg-close').click();
    await page.evaluate(async names=>{for(const n of names)await act('archiveVersion',{VersionID:state.versions.find(v=>v.name===n).id})},names);

    await page.locator('[data-reorganize-main]').click();
    await page.locator('#reorg-form').waitFor();
    await page.locator('#reorg-form [name="products"]').fill('AIGC | AIGC 平台 | http://127.0.0.1:18081/');
    await page.locator('#reorg-form [name="account"]').fill('test-user');
    await page.locator('#reorg-form [name="secret"]').fill('test-secret');
    await page.locator('#reorg-form button[type="submit"]').click();
    await page.locator('#reorg-apply').waitFor();
    const sent=submissions.at(-1);
    const mainCount=await page.evaluate(()=>cases('main').length);
    assert.equal(sent.organize.cases.length,mainCount,'本批包含所有未重整的主线用例');
    assert.equal(sent.requirements,undefined);
    assert.equal(sent.casehubTask,undefined,'CaseHub 自己的字段不转发给节点');
    assert.equal(await page.evaluate(async()=>(await serviceRequest('/api/tasks?kind=planner&subject=reorganize:main')).tasks.length>0),true);
    assert.ok(!(await page.evaluate(()=>localStorage.getItem('casehub-reorg-form'))).includes('test-secret'),'密码不落浏览器存储');
    await page.locator('#reorg-body').getByText('未进入回收站页面').waitFor();

    await page.locator('#reorg-apply').click();
    await page.locator('#reorg-body').getByText('所有用例都已重整').waitFor();
    const after=await page.evaluate(()=>({
      folders:folders('main').map(f=>f.Name),
      cases:cases('main').map(c=>({id:c.ID,path:folderPathNames('main',c.FolderID).join('/'),title:c.Title}))}));
    for(const n of names)assert.ok(!after.folders.includes(n),`版本目录 ${n} 应被删除`);
    for(const c of after.cases)assert.match(c.id,/^TC-AIGC-(LOGIN|ASSET)-FUNC-\d{3}$/,`${c.title} 未改编号`);
    const synced=after.cases.filter(c=>c.title.endsWith(stamp));
    assert.equal(synced.length,2);
    for(const c of synced)assert.equal(c.path,'全部用例/AIGC 平台/资产区/同步');
    assert.equal(new Set(after.cases.map(c=>c.id)).size,after.cases.length,'编号不重复');
    // 归档版本保留旧编号。
    const archived=await page.evaluate(n=>cases(state.versions.find(v=>v.name===n).id).map(c=>c.ID),names[0]);
    assert.ok(archived.every(id=>!id.startsWith('TC-AIGC-')));
    assert.deepEqual(errors,[]);
    console.log('reorganize: ok');
  }finally{
    await browser.close();
    planner.close();
  }
})().catch(e=>{console.error(e);process.exitCode=1});
