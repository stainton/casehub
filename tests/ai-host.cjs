// 「AI 托管」浏览器回归：网页只负责发起，托管由 CaseHub 服务端执行 —— 提交后关掉浏览器，评估 → 设计 → 放入用例评审 →
// 自动评审通过 → 归档到 全部用例/产品/产品版本/需求 仍然完成；停在某一步时重新打开页面可以「重试这一步」。
// 最后从用例管理（右键目录、勾选用例）发起托管：没有设计阶段，直接生成脚本 → 自动执行。
// 只对临时 CASEHUB_STORE=memory 服务运行；脚本自带一个假的 auto-test 服务，同时充当 planner、generator 和 executor：
//   CASEHUB_STORE=memory PORT=18081 CASEHUB_PLANNER_URL=http://127.0.0.1:4598 CASEHUB_GENERATOR_URL=http://127.0.0.1:4598 CASEHUB_EXECUTOR_URL=http://127.0.0.1:4598 go run .
// 生成和执行任务一提交就完成：生成的脚本断言用例标题，标题含「错误」的执行失败，其余通过。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const http=require('node:http');
const {randomUUID}=require('node:crypto');

const BASE=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';
const PLANNER_PORT=Number(process.env.CASEHUB_FAKE_PLANNER_PORT||4598);
const REQ_CODE=`H${Date.now().toString(36).toUpperCase().slice(-6)}`;
const submissions=[];
const jobs=new Map();
// 设计任务一直"进行中"，直到测试调用 release()：这样能在任务跑着的时候关掉浏览器。
function release(){for(const e of jobs.values())if(e.job.status==='running')Object.assign(e.job,{status:'succeeded',stage:'completed',finishedAt:new Date().toISOString()})}
function fakePlanner(){
  const json=(res,status,body)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(body))};
  const read=req=>new Promise(resolve=>{let body='';req.on('data',c=>body+=c);req.on('end',()=>resolve(JSON.parse(body)))});
  return http.createServer(async(req,res)=>{
    const path=req.url.split('?')[0],[,,,,id,resource]=path.split('/');
    if(req.method==='POST'&&path==='/v1/planner/estimate'){
      const input=await read(req);submissions.push({kind:'estimate',input});
      return json(res,200,{suggestedCaseCount:3,rationale:'登录主流程',requirementCodes:[{requirement:input.requirements[0].id,code:REQ_CODE}]});
    }
    if(req.method==='POST'&&path==='/v1/planner/jobs'){
      const input=await read(req);submissions.push({kind:'job',input});
      const jobId=randomUUID(),at=new Date().toISOString(),req0=input.requirements[0].id;
      const c=(mod,n,name)=>({request:req0,name,case_id:`TC-${REQ_CODE}-${mod}-FUNC-00${n}`,priority:'P1',precondition:'已有账号',description:'',steps:'1. 打开登录页',expects:'1. 显示登录表单'});
      const entry={job:{id:jobId,kind:'planner',status:'running',stage:'exploring',createdAt:at,updatedAt:at,startedAt:at,lastEventId:0,timeoutMs:input.timeoutMs},
        result:{reviewStatus:'draft',modules:[{requirement:req0,code:'AUTH',name:'登录认证'},{requirement:req0,code:'PWD',name:'找回密码'}],
          cases:[c('AUTH',1,'正确密码登录成功'),c('AUTH',2,'错误密码登录失败'),c('PWD',1,'邮箱找回密码')],
          explorationNotes:'登录页在 /login',limitations:[{risk:'low',summary:'短信验证码未验证'}],issues:[{risk:'high',scenario:'错误密码',symptom:'没有任何提示'}]}};
      jobs.set(jobId,entry);
      return json(res,202,entry.job);
    }
    if(req.method==='POST'&&(path==='/v1/generator/jobs'||path==='/v1/executor/jobs')){
      const input=await read(req),kind=path.split('/')[2];submissions.push({kind,input});
      const jobId=randomUUID(),at=new Date().toISOString();
      const result=kind==='generator'
        ?{scripts:input.cases.map(c=>({caseId:c.id,fileName:`${c.id}.spec.ts`,language:'typescript',status:'generated',code:`test(${JSON.stringify(c.title)}, async () => {})`,summary:'按用例步骤断言'})),generated:input.cases.length,blocked:0}
        :{passed:!input.title.includes('错误'),markdown:`## ${input.title}\n\n![step](data:image/png;base64,iVBORw0KGgo=)`,output:''};
      const entry={job:{id:jobId,kind,status:'succeeded',stage:'completed',createdAt:at,updatedAt:at,finishedAt:at,lastEventId:0},result};
      jobs.set(jobId,entry);
      return json(res,202,entry.job);
    }
    const entry=jobs.get(id);
    if(!entry)return json(res,404,{error:{code:'JOB_NOT_FOUND',message:'not found'}});
    if(resource==='result')return json(res,200,entry.result);
    if(resource==='events'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(`event: snapshot\ndata: ${JSON.stringify(entry.job)}\n\n`);res.end();return}
    json(res,200,entry.job);
  });
}
const api=async(path,init)=>{const r=await fetch(BASE+path,init);return r.json()};
const hostingOf=async docId=>(await api('/api/hostings')).hostings.find(h=>h.DocID===docId);
async function waitStage(docId,stages,ms=40000){
  const end=Date.now()+ms;
  for(;;){
    const h=await hostingOf(docId);
    if(h&&stages.includes(h.Stage))return h;
    if(Date.now()>end)throw Error(`hosting stage ${h?.Stage} (${h?.Error||''}), want ${stages}`);
    await new Promise(r=>setTimeout(r,200));
  }
}
async function openPage(browser){
  const page=await browser.newPage({viewport:{width:1500,height:900}}),errors=[];
  page.on('pageerror',e=>errors.push(e.stack||e.message));
  page.on('dialog',d=>d.accept());
  await page.goto(BASE);
  await page.waitForFunction(()=>typeof state!=='undefined'&&state&&state.folders);
  return {page,errors};
}
async function openHostDrawer(page,docId){
  await page.locator('.page-tab[data-app="requirements"]').click();
  await page.evaluate(id=>setReqFocus({type:'doc',id}),docId); // 嵌套目录默认折叠
  await page.locator('#req-ai-host').click();
}

(async()=>{
  const planner=fakePlanner();
  await new Promise(resolve=>planner.listen(PLANNER_PORT,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
  try{
    let {page,errors}=await openPage(browser);
    const title=`托管需求 ${Date.now()}`,VNAME=`托管目标 ${Date.now()}`;
    // 需求放在需求树的 产品/版本 目录下：托管表单用它预填归档位置。
    const {docId,vid}=await page.evaluate(async([t,vn])=>{
      let out=await act('createReqFolder',{ParentID:'req-root',Name:'TaskLite'});
      const p=out.state.reqFolders.find(f=>f.Name==='TaskLite'&&f.ParentID==='req-root').ID;
      out=await act('createReqFolder',{ParentID:p,Name:'v2.0'});
      const v=out.state.reqFolders.find(f=>f.Name==='v2.0'&&f.ParentID===p).ID;
      await act('createReqDoc',{FolderID:v,Title:t,Content:'登录成功跳转首页；错误密码显示提示；可通过邮箱找回密码。'});
      await act('createVersion',{Name:vn,Blank:true});
      return {docId:state.reqDocs.find(d=>d.Title===t).ID,vid:state.versions.find(x=>x.name===vn).id};
    },[title,VNAME]);

    await openHostDrawer(page,docId);
    assert.equal(await page.locator('#req-ai-design + #req-ai-host').count(),1,'AI 托管 sits right next to AI 设计');
    await page.locator('#host-form').waitFor();
    assert.equal(await page.locator('#ai-drawer .panel-head strong').innerText(),'AI 托管');
    assert.equal(await page.locator('#host-form [name="product"]').inputValue(),'TaskLite','product prefilled from the requirement tree');
    assert.equal(await page.locator('#host-form [name="productVersion"]').inputValue(),'v2.0');
    await page.locator('#host-form [name="versionID"]').selectOption(vid);
    await page.locator('#host-form [name="baseUrl"]').fill('http://127.0.0.1:4500');
    await page.locator('#host-form [name="testAccount"]').fill('tester');
    await page.locator('#host-form [name="testSecret"]').fill('s3cret');
    assert.match(await page.locator('#host-path').innerText(),new RegExp(`全部用例 / TaskLite / v2.0 / ${title}`));
    await page.locator('#host-form [name="withScripts"]').uncheck(); // 前两个场景只托管到归档，脚本生成在最后一个场景
    assert.equal(await page.locator('#host-form [name="withRun"]').isChecked(),false,'running needs scripts');
    await page.locator('#host-form button[type="submit"]').click();
    await waitStage(docId,['designing']);
    await page.locator('#ai-log').waitFor(); // 设计阶段显示 planner 进度
    await page.waitForFunction(()=>document.querySelector('#req-ai-host').textContent==='AI 托管中');
    assert.deepEqual(errors,[]);

    // 关掉浏览器（任务仍在设计中）：之后的步骤全在服务端完成。
    await page.context().close();
    release();
    let h=await waitStage(docId,['done','stopped']);
    assert.equal(h.Stage,'done',h.Error);
    assert.deepEqual(submissions.map(s=>s.kind),['estimate','job'],'the estimate is adopted, not re-run');
    const job=submissions[1].input;
    assert.equal(job.caseCount,3,'the estimated count is used as-is');
    assert.equal(job.requirements[0].code,REQ_CODE,'the suggested code is sent');
    assert.equal(job.context.testData.password,'s3cret','the test account reaches the planner');
    const st=await api('/api/state');
    assert.ok(!JSON.stringify(st).includes('s3cret'),'the test password is never stored');
    const f=(parent,name)=>st.folders.find(x=>x.VersionID===vid&&(x.ParentID||'')===parent&&x.Name===name);
    const prod=f('root','TaskLite'),ver=prod&&f(prod.ID,'v2.0'),reqF=ver&&f(ver.ID,title);
    assert.ok(reqF,'filed under 全部用例 / TaskLite / v2.0 / <requirement>');
    const under=new Set([reqF.ID]);for(let grew=true;grew;){grew=false;for(const x of st.folders)if(x.VersionID===vid&&under.has(x.ParentID)&&!under.has(x.ID)){under.add(x.ID);grew=true}}
    assert.deepEqual(st.cases.filter(c=>c.VersionID===vid&&under.has(c.FolderID)).map(c=>c.Title).sort(),['正确密码登录成功','邮箱找回密码','错误密码登录失败'].sort());
    assert.equal(st.pendingCases.filter(c=>h.CaseIDs.includes(c.ID)).length,0,'the review area no longer holds them');
    assert.equal(st.reqDocs.find(d=>d.ID===docId).Code,REQ_CODE);
    assert.equal(st.reqDocs.find(d=>d.ID===docId).ExplorationNotes,'登录页在 /login');
    assert.ok(st.issues.some(i=>i.RequirementID===docId&&i.Title==='[高] 错误密码'),'defects go to 问题单管理');

    // 重新打开页面：抽屉显示完成的托管。
    ({page,errors}=await openPage(browser));
    await openHostDrawer(page,docId);
    await page.locator('.host-card').waitFor();
    assert.equal(await page.locator('.host-step').count(),5,'no script steps when not asked for');
    assert.equal(await page.locator('.host-step.done').count(),5);
    assert.match(await page.locator('#ai-drawer-body').innerText(),/已完成：3 条用例已评审通过并归档/);

    // 再托管一次：归档时目标需求目录里已有同名用例 → 停在「归档到用例树」；清掉冲突后在页面上「重试这一步」。
    await page.locator('#host-again').click();
    assert.equal(await page.locator('#host-form [name="withScripts"]').isChecked(),false,'the scope is remembered');
    await page.locator('#host-form [name="versionID"]').selectOption(vid);
    await page.locator('#host-form [name="baseUrl"]').fill('http://127.0.0.1:4500');
    await page.locator('#host-form button[type="submit"]').click();
    await waitStage(docId,['designing']);
    release();
    h=await waitStage(docId,['stopped']);
    assert.equal(h.FailedAt,'archiving',JSON.stringify(h));
    assert.match(h.Error,/同名用例/);
    await page.locator('#host-resume').waitFor();
    // 任务中心的「AI 托管」页签：停下的托管在「进行中」里，标明需处理；点开回到它的托管视图。
    await page.locator('.tc-tab[data-tc="host"]').click();
    const row=page.locator(`#ai-drawer-body [data-tc-host="${docId}"]`);
    await row.waitFor();
    assert.match(await row.innerText(),/已停止于「归档到用例树」，需处理/);
    assert.match(await row.innerText(),new RegExp(`TaskLite / v2.0 / ${title}`));
    assert.match(await page.locator('.tc-tab[data-tc="host"]').innerText(),/AI 托管\s*1/,'the tab counts unfinished hostings');
    await row.click();
    await page.locator('#host-resume').waitFor();
    assert.match(await page.locator('#host-resume').innerText(),/重试这一步/);
    const vcases=(await api('/api/state')).cases.filter(c=>c.VersionID===vid).map(c=>c.ID);
    await page.evaluate(([vid,ids])=>act('deleteCases',{VersionID:vid,CaseIDs:ids}),[vid,vcases]);
    await page.locator('#host-resume').click();
    h=await waitStage(docId,['done','stopped']);
    assert.equal(h.Stage,'done',h.Error);
    assert.equal(submissions.filter(s=>s.kind==='job').length,2,'retrying a step does not redesign');
    const st2=await api('/api/state');
    assert.equal(st2.folders.filter(x=>x.VersionID===vid&&x.Name==='TaskLite').length,1,'the product folder is reused');
    assert.equal(st2.cases.filter(c=>c.VersionID===vid).length,3);
    await page.waitForFunction(()=>document.querySelectorAll('.host-step.done').length===5);

    // 还没有合适的测试版本时，表单里直接「＋ 新建测试版本…」：开始托管时创建一个空版本作为归档目标。
    const title2=`新版本托管 ${Date.now()}`;
    const doc2=await page.evaluate(async t=>{await act('createReqDoc',{FolderID:'req-root',Title:t,Content:'登录'});return state.reqDocs.find(d=>d.Title===t).ID},title2);
    await page.locator('#ai-drawer-close').click();
    await openHostDrawer(page,doc2);
    await page.locator('#host-form').waitFor();
    await page.locator('#host-form [name="versionID"]').selectOption('__new__');
    await page.locator('#host-form [name="product"]').fill('NewProd');
    await page.locator('#host-form [name="productVersion"]').fill('v9');
    await page.locator('#host-form [name="withRun"]').check(); // 勾执行会带上生成
    assert.equal(await page.locator('#host-form [name="newVersionName"]').inputValue(),'NewProd v9 测试','the new version is named after the target');
    await page.locator('#host-form [name="baseUrl"]').fill('http://127.0.0.1:4500');
    await page.locator('#host-form button[type="submit"]').click();
    await waitStage(doc2,['designing']);
    release();
    h=await waitStage(doc2,['done','stopped']);
    assert.equal(h.Stage,'done',h.Error);
    const st3=await api('/api/state'),nv=st3.versions.find(v=>v.id===h.VersionID);
    assert.equal(nv?.name,'NewProd v9 测试');
    assert.ok(st3.folders.some(f=>f.VersionID===nv.id&&f.Name==='NewProd')&&st3.cases.filter(c=>c.VersionID===nv.id).length===3);
    // 归档之后：为 3 条用例生成脚本，逐条执行，保存测试记录（含截图），统计通过/失败。
    assert.equal(h.Generated,3);
    assert.deepEqual(Object.values(h.Results).sort(),['failed','passed','passed']);
    const gen=submissions.filter(x=>x.kind==='generator').at(-1).input;
    assert.equal(gen.cases.length,3);
    assert.equal(gen.requirements[0].id,doc2,'the requirement goes to the generator');
    assert.equal(gen.target.baseUrl,'http://127.0.0.1:4500');
    assert.equal(submissions.filter(x=>x.kind==='executor').length,3);
    assert.equal(st3.scripts.filter(x=>x.VersionID===nv.id).length,3,'the scripts are saved in the version');
    const recs=st3.records.filter(r=>r.VersionID===nv.id&&r.Submitted);
    assert.equal(recs.length,3);
    assert.equal(recs.filter(r=>r.Result==='failed').length,1);
    assert.ok(recs.every(r=>r.Note.includes('AI 托管自动执行的脚本')&&!r.Note.includes('data:image')),'screenshots move into record images');
    await page.waitForFunction(()=>document.querySelectorAll('.host-step.done').length===7);
    assert.match(await page.locator('#ai-drawer-body').innerText(),/生成自动化脚本：3 个脚本/);
    assert.match(await page.locator('#ai-drawer-body').innerText(),/自动化执行：通过 2 · 失败 1/);
    // 同一缩写已被第一份需求占用：托管自动改用下一个可用的（…2），不会停下。
    assert.equal(st3.reqDocs.find(d=>d.ID===doc2).Code,`${REQ_CODE}2`);
    await page.waitForFunction(code=>document.querySelector('#req-doc-code')?.textContent===code,`${REQ_CODE}2`);
    // 两次托管都完成后：「进行中」为空，「已完成」里各有一行。
    await page.locator('#open-task-center').click();
    await page.locator('.tc-tab[data-tc="host"]').click();
    await page.waitForFunction(()=>document.querySelector('.tc-filter')?.textContent.includes('进行中 0'));
    assert.equal(await page.locator('#ai-drawer-body [data-tc-host]').count(),0);
    await page.locator('[data-tc-filter="done"]').click();
    assert.equal(await page.locator(`#ai-drawer-body [data-tc-host="${docId}"], #ai-drawer-body [data-tc-host="${doc2}"]`).count(),2);
    assert.match(await page.locator(`#ai-drawer-body [data-tc-host="${doc2}"]`).innerText(),/已完成 · 3 条 · 通过 2 · 失败 1/);
    await page.locator('.tc-tab[data-tc="plan"]').click();
    assert.equal(await page.locator('#ai-drawer .panel-head strong').innerText(),'AI 设计','switching back restores the planner header');

    // 用例管理里发起托管：右键目录 →「AI 托管」，没有设计阶段，直接生成脚本 → 自动执行；用例编号前缀匹配需求文档。
    await page.locator('#ai-drawer-close').click();
    const VN4=`用例托管 ${Date.now()}`,plannerJobs=submissions.filter(x=>x.kind==='job').length;
    const {vid4,fid4}=await page.evaluate(async([vn,code])=>{
      let out=await act('createVersion',{Name:vn,Blank:true});
      const vid=out.state.versions.find(x=>x.name===vn).id;
      out=await act('createFolder',{VersionID:vid,ParentID:'root',Name:'登录回归'});
      const fid=out.state.folders.find(f=>f.VersionID===vid&&f.Name==='登录回归').ID;
      for(const [id,title,steps] of [[`TC-${code}-AUTH-FUNC-101`,'登录成功','1. 登录'],[`TC-${code}-AUTH-FUNC-102`,'错误密码提示','1. 输错密码'],['CASE-NOSTEP','没有步骤','']])
        await act('createCase',{VersionID:vid,FolderID:fid,CaseID:id,Title:title,Steps:steps});
      return {vid4:vid,fid4:fid};
    },[VN4,REQ_CODE]);
    await page.locator('.page-tab[data-app="cases"]').click();
    await page.evaluate(([v,f])=>setFocus({type:'folder',versionID:v,id:f}),[vid4,fid4]);
    await page.locator(`.folder-row[data-version="${vid4}"][data-folder="${fid4}"]`).first().click({button:'right'});
    await page.locator('#context-menu button',{hasText:'AI 托管'}).click();
    await page.locator('#host-form').waitFor();
    assert.match(await page.locator('#ai-drawer-body').innerText(),/直接为这 2 条用例生成自动化脚本[\s\S]*另外 1 条没有执行步骤/);
    assert.equal(await page.locator('#host-form [name="product"]').count(),0,'nothing to file: no archive target');
    await page.locator('#host-form [name="baseUrl"]').fill('http://127.0.0.1:4500');
    await page.locator('#host-form [name="testSecret"]').fill('case-s3cret');
    await page.locator('#host-form [name="withRun"]').check();
    await page.locator('#host-form button[type="submit"]').click();
    const caseHosting=async()=>(await api('/api/hostings')).hostings.filter(x=>x.Source==='cases'&&x.VersionID===vid4);
    for(const end=Date.now()+40000;;){const [x]=await caseHosting();if(x&&['done','stopped'].includes(x.Stage)){h=x;break}if(Date.now()>end)throw Error('case hosting did not finish');await new Promise(r=>setTimeout(r,200))}
    assert.equal(h.Stage,'done',h.Error);
    assert.equal(h.Label,'登录回归');
    assert.equal(submissions.filter(x=>x.kind==='job').length,plannerJobs,'hosting cases does not design');
    const gen4=submissions.filter(x=>x.kind==='generator').at(-1).input;
    assert.deepEqual(gen4.cases.map(c=>c.id),[`TC-${REQ_CODE}-AUTH-FUNC-101`,`TC-${REQ_CODE}-AUTH-FUNC-102`]);
    assert.equal(gen4.cases[0].requirement,docId,'the case code picks its requirement');
    assert.equal(gen4.requirements[0].id,docId);
    assert.equal(gen4.context.testData.password,'case-s3cret');
    assert.deepEqual(Object.values(h.Results).sort(),['failed','passed']);
    assert.ok(!JSON.stringify(await api('/api/state')).includes('case-s3cret'),'the test password is never stored');
    await page.waitForFunction(()=>document.querySelectorAll('.host-step.done').length===2);
    assert.equal(await page.locator('.host-step').count(),2,'only generation and run');
    assert.match(await page.locator('#ai-drawer-body').innerText(),/已完成：生成 2 个脚本，自动执行 通过 1 · 失败 1/);

    // 勾选用例 → 批量栏「AI 托管」：只生成脚本。
    await page.locator('#ai-drawer-close').click();
    await page.locator(`.case-row[data-version="${vid4}"][data-case="TC-${REQ_CODE}-AUTH-FUNC-101"] .case-check`).first().check();
    await page.locator('#bulk [data-bulk="host"]').click();
    await page.locator('#host-form').waitFor();
    assert.match(await page.locator('#ai-drawer-body').innerText(),/这 1 条用例[\s\S]*其中 1 条已有脚本，会重新生成/);
    await page.locator('#host-form [name="withRun"]').uncheck();
    await page.locator('#host-form button[type="submit"]').click();
    await page.waitForFunction(()=>document.querySelector('#ai-drawer-body')?.textContent.includes('已完成：生成 1 个脚本'));
    assert.equal(await page.locator('#bulk').isHidden(),true,'the selection is cleared once hosted');
    const hs=await caseHosting();
    assert.equal(hs.length,2,'a hosting of fewer cases keeps the folder one');
    assert.equal(hs.find(x=>x.Label==='已选 1 条用例')?.Execute,false);
    // 任务中心的「AI 托管」页签同样列出用例托管，点开回到它的托管视图。
    await page.locator('#open-task-center').click();
    await page.locator('.tc-tab[data-tc="host"]').click();
    await page.locator('[data-tc-filter="done"]').click();
    const crow=page.locator(`#ai-drawer-body [data-tc-host-cases="${h.ID}"]`);
    assert.match(await crow.innerText(),/登录回归[\s\S]*2 条用例/);
    await crow.click();
    await page.waitForFunction(()=>document.querySelectorAll('.host-step.done').length===2);
    assert.equal(await page.locator('#ai-drawer-doc').innerText(),`${VN4} · 登录回归`);
    assert.deepEqual(errors,[]);
    console.log('ai host ok');
  }finally{await browser.close();planner.close()}
})().catch(e=>{console.error(e);process.exit(1)});
