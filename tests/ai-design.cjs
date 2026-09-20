// 「AI 设计」抽屉的浏览器回归：失败后保留表单、重试与继续。只对临时 CASEHUB_STORE=memory 服务运行。
// 脚本自带一个假的 planner 服务（真实的 HTTP 形状，不调用模型）：启动 CaseHub 时用
// CASEHUB_PLANNER_URL 指向它即可，不需要 auto-test 仓库在场。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const http=require('node:http');
const {randomUUID}=require('node:crypto');

const PLANNER_PORT=Number(process.env.CASEHUB_FAKE_PLANNER_PORT||4598);
// 需求缩写在 CaseHub 里必须唯一，重复运行脚本会撞车，所以每次跑用一个新的。
const REQ_CODE=`L${Date.now().toString(36).toUpperCase().slice(-6)}`;
const submissions=[];
function fakePlanner(){
  const jobs=new Map();
  const json=(res,status,body)=>{res.writeHead(status,{'Content-Type':'application/json','Access-Control-Allow-Origin':'*'});res.end(JSON.stringify(body))};
  const read=req=>new Promise(resolve=>{let body='';req.on('data',c=>body+=c);req.on('end',()=>resolve(JSON.parse(body)))});
  return http.createServer(async(req,res)=>{
    const [,,,,id,resource]=req.url.split('/');
    if(req.method==='POST'&&req.url==='/v1/planner/estimate'){
      const input=await read(req);
      submissions.push({kind:'estimate',input});
      return json(res,200,{suggestedCaseCount:8,rationale:'覆盖登录与找回密码主流程',
        requirementCodes:[{requirement:input.requirements[0].id,code:REQ_CODE}]});
    }
    if(req.method==='POST'&&req.url==='/v1/planner/jobs'){
      const input=await read(req);
      submissions.push({kind:'job',input});
      const jobId=randomUUID(),at=new Date().toISOString();
      const base={id:jobId,kind:'planner',status:'failed',stage:'failed',createdAt:at,updatedAt:at,finishedAt:at,lastEventId:1,timeoutMs:input.timeoutMs};
      // 第一次提交以 JOB_TIMEOUT 失败但会话仍在（continuable）；带 continueFrom 的继续提交成功。
      const entry=input.continueFrom
        ?{job:{...base,status:'succeeded',stage:'completed'},
          result:{reviewStatus:'draft',modules:[{requirement:input.requirements[0].id,code:'AUTH',name:'登录认证'}],
            cases:[{request:input.requirements[0].id,name:'错误密码登录失败',case_id:`TC-${REQ_CODE}-AUTH-FUNC-001`,priority:'P1',
              precondition:'已存在测试账号',description:'',steps:'1. 打开登录页',expects:'1. 显示登录表单'}],
            explorationNotes:'',limitations:[],issues:[]}}
        :{job:{...base,continuable:true,error:{code:'JOB_TIMEOUT',message:'Planner task exceeded its time limit of 3 minutes'}}};
      jobs.set(jobId,entry);
      res.setHeader('Location',`/v1/planner/jobs/${jobId}`);
      return json(res,202,entry.job);
    }
    const entry=jobs.get(id);
    if(!entry)return json(res,404,{error:{code:'JOB_NOT_FOUND',message:'not found'}});
    if(resource==='result')return json(res,200,entry.result);
    if(resource==='events'){
      res.writeHead(200,{'Content-Type':'text/event-stream','Access-Control-Allow-Origin':'*'});
      res.write(`event: snapshot\ndata: ${JSON.stringify(entry.job)}\n\n`);
      res.end();
      return;
    }
    json(res,200,entry.job);
  });
}

const FORM={baseUrl:'http://127.0.0.1:18081',instructions:'提交后确认任务已开始即可，不要等待任务运行完成',
  testAccount:'test-user',testSecret:'test-secret'};
async function openDrawer(page,docId){
  await page.locator('.page-tab[data-app="requirements"]').click();
  await page.locator(`#req-tree [data-req-doc="${docId}"]`).click();
  await page.locator('#req-ai-design').click();
  await page.locator('#ai-drawer').waitFor();
}
async function fillForm(page){
  for(const [name,value] of Object.entries(FORM))await page.locator(`#ai-form [name="${name}"]`).fill(value);
}

let page=null;
(async()=>{
  const planner=fakePlanner();
  await new Promise(resolve=>planner.listen(PLANNER_PORT,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
  try{
    page=await browser.newPage();const errors=[];
    page.on('pageerror',e=>errors.push(e.stack||e.message));
    await page.goto(process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081');
    await page.locator('.folder-row').first().waitFor();
    const title=`AI 设计验证 ${Date.now()}`;
    const docId=await page.evaluate(async t=>{
      await act('createReqDoc',{FolderID:'req-root',Title:t,Content:'登录成功跳转首页；错误密码显示提示。'});
      return state.reqDocs.find(d=>d.Title===t).ID;
    },title);

    // 评估 → 确认 → 任务超时失败
    await openDrawer(page,docId);
    await page.locator('#ai-agent').waitFor();
    assert.equal(await page.locator('#ai-agent').inputValue(),'');
    assert.equal(await page.locator('#ai-form').count(),0);
    assert.equal(submissions.length,0);
    await page.locator('#ai-agent').selectOption('playwright');
    await fillForm(page);
    await page.locator('#ai-form [name="timeoutMinutes"]').fill('22');
    // 取消选择会隐藏参数和操作；重新选择保留已填写的内容。
    await page.locator('#ai-agent').selectOption('');
    assert.equal(await page.locator('#ai-form').count(),0);
    await page.locator('#ai-agent').selectOption('playwright');
    assert.equal(await page.locator('#ai-form [name="baseUrl"]').inputValue(),FORM.baseUrl);
    assert.equal(await page.locator('#ai-form [name="timeoutMinutes"]').inputValue(),'22');
    await page.locator('#ai-drawer-close').click();
    await openDrawer(page,docId);
    assert.equal(await page.locator('#ai-agent').inputValue(),'playwright');
    await page.locator('#ai-form button[type="submit"]').click(); // 开始分析
    await page.locator('#ai-form [name="caseCount"]:not([disabled])').waitFor();
    assert.equal(await page.locator('#ai-form [name="caseCount"]').inputValue(),'8');
    assert.equal(await page.locator('#ai-form [name="reqCode"]').inputValue(),REQ_CODE);
    assert.match(await page.locator('#ai-drawer-body').innerText(),/覆盖登录与找回密码主流程/);
    await page.locator('#ai-form [name="timeoutMinutes"]').fill('3');
    await page.locator('#ai-form button[type="submit"]').click(); // 确认并开始设计
    await page.locator('#ai-continue').waitFor();
    assert.match(await page.locator('#ai-drawer-body').innerText(),/JOB_TIMEOUT/);
    assert.equal(await page.locator('#ai-retry').count(),1);

    // 新表单记录 agent；去掉该字段模拟旧版数据，刷新后仍可恢复 Playwright。
    await page.evaluate(id=>{
      const key=`casehub-ai-form-${id}`,saved=JSON.parse(localStorage.getItem(key));
      if(saved.values.agentId!=='playwright'||saved.values.testSecret!=='')throw Error('保存的 agent 或密码不正确');
      delete saved.values.agentId;
      localStorage.setItem(key,JSON.stringify(saved));
    },docId);
    // 失败状态在刷新后仍然可见：继续和重试都还在
    await page.reload();
    await page.locator('.folder-row').first().waitFor();
    await openDrawer(page,docId);
    await page.locator('#ai-continue').waitFor();

    // 重试：表单还是上次提交的内容（密码不写进浏览器存储，需要重填）
    await page.locator('#ai-retry').click();
    await page.locator('#ai-form').waitFor();
    assert.equal(await page.locator('#ai-agent').inputValue(),'playwright');
    assert.equal(await page.locator('#ai-form [name="baseUrl"]').inputValue(),FORM.baseUrl);
    assert.equal(await page.locator('#ai-form [name="instructions"]').inputValue(),FORM.instructions);
    assert.equal(await page.locator('#ai-form [name="testAccount"]').inputValue(),FORM.testAccount);
    assert.equal(await page.locator('#ai-form [name="testSecret"]').inputValue(),'');
    assert.equal(await page.locator('#ai-form [name="caseCount"]').inputValue(),'8');
    assert.equal(await page.locator('#ai-form [name="reqCode"]').inputValue(),REQ_CODE);
    assert.match(await page.locator('#ai-drawer-body').innerText(),/密码没有保存在浏览器里/);
    assert.equal(await page.locator('#ai-form button[type="submit"]').innerText(),'确认并开始设计');

    // 重试清掉了失败任务，所以重新走一遍失败，再从「继续」提交
    await page.locator('#ai-form [name="testSecret"]').fill(FORM.testSecret);
    await page.locator('#ai-form button[type="submit"]').click();
    await page.locator('#ai-continue').waitFor();
    const failedJobId=await page.evaluate(id=>localStorage.getItem(`casehub-ai-job-${id}`),docId);
    await page.locator('#ai-continue').click();
    await page.locator('#ai-form').waitFor();
    assert.equal(await page.locator('#ai-agent').inputValue(),'playwright');
    assert.equal(await page.locator('#ai-agent').isDisabled(),true);
    assert.match(await page.locator('#ai-drawer-body').innerText(),/继续上次中断的设计/);
    assert.equal(await page.locator('#ai-form [name="testSecret"]').inputValue(),FORM.testSecret); // 同一页面会话里密码还在
    assert.equal(await page.locator('#ai-form button[type="submit"]').innerText(),'继续设计');
    await page.locator('#ai-form [name="timeoutMinutes"]').fill('30'); // 继续时可以调大时限
    await page.locator('#ai-form button[type="submit"]').click();
    await page.locator('#ai-restart').waitFor();
    assert.match(await page.locator('#ai-drawer-body').innerText(),/已导入用例\s*1/);

    const continued=submissions.filter(s=>s.kind==='job').at(-1).input;
    assert.equal(continued.continueFrom,failedJobId);
    assert.equal(continued.timeoutMs,1800000);
    assert.equal(continued.caseCount,8);
    assert.equal(continued.target.baseUrl,FORM.baseUrl);
    assert.match(continued.context.instructions,/不要等待任务运行完成/);
    assert.equal(continued.context.testData.password,FORM.testSecret);

    // 草稿进了「用例评审」
    await page.locator('#ai-drawer-close').click();
    await page.locator('#req-sidebar [data-reqview="review"]').click();
    assert.match(await page.locator('#review-tree').innerText(),/登录认证/);
    assert.deepEqual(errors,[]);
    console.log('AI 设计抽屉回归通过');
  }finally{
    await browser.close();
    planner.closeAllConnections();
    await new Promise(resolve=>planner.close(resolve));
  }
})().catch(e=>{console.error(e);process.exit(1)});
