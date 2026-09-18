// 脚本生成 + 自动化管理 的浏览器回归。只对临时 CASEHUB_STORE=memory 服务运行。
// 本脚本自带一个假的 generator 服务（真实的 HTTP 形状，不调用模型）：启动 CaseHub 时
// 用 CASEHUB_GENERATOR_URL 指向它即可，不需要 auto-test 仓库在场。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const http=require('node:http');
const {randomUUID}=require('node:crypto');

const GEN_PORT=Number(process.env.CASEHUB_FAKE_GENERATOR_PORT||4599);
function fakeGenerator(){
  const jobs=new Map();
  const json=(res,status,body)=>{res.writeHead(status,{'Content-Type':'application/json','Access-Control-Allow-Origin':'*'});res.end(JSON.stringify(body))};
  return http.createServer((req,res)=>{
    const [,,,, id,resource]=req.url.split('/');
    if(req.method==='POST'&&req.url==='/v1/generator/jobs'){
      let body='';
      req.on('data',c=>body+=c);
      req.on('end',()=>{
        const input=JSON.parse(body),jobId=randomUUID();
        // 第二条用例固定返回 blocked，用来验证"受阻"分支照样保存并展示原因。
        const scripts=input.cases.map((c,i)=>i===1
          ?{caseId:c.id,title:c.title,fileName:`${c.id}.spec.ts`,language:'typescript',status:'blocked',code:'',summary:'缺少可用的测试账号',deviations:[]}
          :{caseId:c.id,title:c.title,fileName:`${c.id}.spec.ts`,language:'typescript',status:'generated',summary:`验证${c.title}`,
            deviations:[{risk:'medium',summary:'错误提示文案与用例不同'}],
            code:`import { test, expect } from '@playwright/test';\ntest('${c.id}', async ({ page }) => { await page.goto('/'); });\n`});
        const blocked=scripts.filter(s=>s.status==='blocked').length;
        jobs.set(jobId,{job:{id:jobId,kind:'generator',status:'succeeded',stage:'completed',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString(),lastEventId:1},
          result:{scripts,generated:scripts.length-blocked,blocked,explorationNotes:'',limitations:scripts.filter(s=>s.status==='blocked').map(s=>({risk:'high',summary:s.summary}))},
          requirements:input.requirements||[]});
        res.setHeader('Location',`/v1/generator/jobs/${jobId}`);
        json(res,202,jobs.get(jobId).job);
      });
      return;
    }
    const entry=jobs.get(id);
    if(!entry)return json(res,404,{error:{code:'JOB_NOT_FOUND',message:'not found'}});
    if(resource==='result')return json(res,200,entry.result);
    if(resource==='events'){
      res.writeHead(200,{'Content-Type':'text/event-stream','Access-Control-Allow-Origin':'*'});
      res.write(`event: snapshot\ndata: ${JSON.stringify(entry.job)}\n\n`);
      res.write(`id: 1\nevent: progress\ndata: ${JSON.stringify({id:1,jobId:id,status:'succeeded',stage:'completed',message:'Generated scripts',createdAt:new Date().toISOString()})}\n\n`);
      res.end();
      return;
    }
    json(res,200,entry.job);
  });
}

(async()=>{
  const generator=fakeGenerator();
  await new Promise(resolve=>generator.listen(GEN_PORT,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
  try{
    const page=await browser.newPage(),errors=[];
    page.on('pageerror',e=>errors.push(e.stack||e.message));
    await page.goto(process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081');
    await page.locator('.folder-row').first().waitFor();
    const versionName=`自动化验证 ${Date.now()}`;
    const versionId=await page.evaluate(async name=>{
      await act('createVersion',{Name:name});
      return state.versions.find(v=>v.name===name).id;
    },versionName);

    // 目录右键 → 脚本生成：批量为目录下所有用例生成
    await page.locator(`.version[data-version="${versionId}"] .folder-row`).first().click({button:'right'});
    await page.locator('#context-menu button',{hasText:'脚本生成'}).click();
    await page.locator('#gen-form [name="baseUrl"]').fill('http://127.0.0.1:18081');
    await page.locator('#gen-form button[type="submit"]').click();
    await page.locator('#gen-drawer-body .gen-chip',{hasText:'已完成'}).first().waitFor();
    assert.match(await page.locator('#gen-drawer-body').innerText(),/已生成\s*1/);
    assert.match(await page.locator('#gen-drawer-body').innerText(),/缺少可用的测试账号/); // 受阻原因可见

    // 生成结果同步到「自动化管理」：目录同名同构，只显示已生成脚本的用例
    await page.locator('#gen-drawer-close').click();
    await page.locator('.page-tab[data-app="automation"]').click();
    const tree=page.locator('#script-tree');
    assert.match(await tree.innerText(),/全部用例/);
    assert.match(await tree.innerText(),/CASE-0001/);
    await tree.locator('[data-script-case="CASE-0001"]').click();
    assert.match(await page.locator('#auto-detail').innerText(),/CASE-0001\.spec\.ts/);
    assert.match(await page.locator('#auto-detail').innerText(),/@playwright\/test/);
    assert.match(await page.locator('#auto-detail').innerText(),/错误提示文案与用例不同/); // 实测偏差

    // 用例改动后脚本判定为过时
    await page.evaluate(async v=>{
      const c=state.cases.find(x=>x.VersionID===v&&x.ID==='CASE-0001');
      await act('editCase',{VersionID:v,CaseID:c.ID,Title:c.Title,Priority:c.Priority,Preconditions:c.Preconditions,Steps:`${c.Steps}\n9. 新增一步`,Expected:c.Expected});
    },versionId);
    assert.match(await tree.locator('[data-script-case="CASE-0001"]').innerText(),/过时/);
    await tree.locator('[data-script-case="CASE-0001"]').click();
    assert.match(await page.locator('#auto-detail').innerText(),/用例内容已变更/);

    // 重新生成：同一条用例仍然只有一个脚本，且不再过时
    await page.locator('#script-regen-stale').click();
    await page.locator('#gen-form button[type="submit"]').click();
    await page.locator('#gen-drawer-body [data-gen-panel]').first().locator('.gen-chip',{hasText:'已完成'}).waitFor();
    assert.equal(await page.evaluate(v=>state.scripts.filter(s=>s.VersionID===v).length,versionId),2);
    assert.doesNotMatch(await tree.locator('[data-script-case="CASE-0001"]').innerText(),/过时/);
    // 抽屉里两个任务各自是一个可展开选项卡
    assert.equal(await page.locator('#gen-drawer-body [data-gen-panel]').count(),2);

    // 删除脚本不影响用例
    page.once('dialog',d=>d.accept());
    await tree.locator('[data-script-case="CASE-0001"]').click({button:'right'});
    await page.locator('#context-menu button',{hasText:'删除脚本'}).click();
    await page.waitForFunction(v=>state.scripts.filter(s=>s.VersionID===v).length===1,versionId);
    // 删的是脚本，用例本身还在（别用用例总数断言：同一台临时服务上别的回归脚本可能往主线合过用例）
    assert.equal(await page.evaluate(v=>cases(v).some(c=>c.ID==='CASE-0001'),versionId),true);

    assert.deepEqual(errors,[]);
    console.log('PASS: 目录批量生成、受阻原因、脚本树同构、过时判定、重新生成、多任务选项卡和删除脚本');
  }finally{
    await browser.close();
    await new Promise(resolve=>generator.close(resolve));
  }
})().catch(e=>{console.error(e);process.exit(1)});
