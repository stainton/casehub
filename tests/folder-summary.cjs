// 目录「总结测试报告」浏览器回归：结果统计由前端算、需求与未解决问题单随请求带给通用 AI、风险项写入下载的 Markdown。
// 通用 AI 服务用 page.route 拦截成固定结果（不调用模型）。只对临时 CASEHUB_STORE=memory 服务运行。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const BASE=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';
const CODE=`S${Date.now().toString(36).toUpperCase().slice(-6)}`;

(async()=>{
  const browser=await chromium.launch(),page=await browser.newPage({viewport:{width:1500,height:900}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const action=async body=>{const r=await page.evaluate(async b=>(await fetch('/api/action',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b)})).json(),body);assert.ok(!r.error,r.error);return r};
  await page.goto(BASE);
  const doc=(await page.evaluate(async()=>(await fetch('/api/state')).json())).reqDocs[0];
  await action({Type:'setReqDocCode',DocID:doc.ID,Code:CODE});
  await action({Type:'createIssue',Title:`登录偶发超时 ${CODE}`,RequirementID:doc.ID,Description:'高峰期登录超时'});
  const v=(await action({Type:'createVersion',Name:`总结 ${CODE}`})).state.versions.find(x=>x.name===`总结 ${CODE}`);
  const ids=[`TC-${CODE}-A-1`,`TC-${CODE}-A-2`,'FREE-3'];
  for(const [i,id] of ids.entries())await action({Type:'createCase',VersionID:v.id,FolderID:'auth',CaseID:id,Title:`用例${i+1}`,Priority:'P1',Steps:'s',Expected:'e'});
  await action({Type:'submitRecord',VersionID:v.id,CaseID:ids[0],Result:'passed',Note:'ok'});
  await action({Type:'submitRecord',VersionID:v.id,CaseID:ids[1],Result:'failed',Note:'提示不正确 ![x](data:image/png;base64,AAAA)'});

  let prompt,release;const gate=new Promise(r=>release=r);
  await page.route('**/api/general-agent/generate',async route=>{
    prompt=JSON.parse(JSON.parse(route.request().postData()).prompt);
    await gate;
    route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({output:{conclusion:'整体可用，存在失败项。',features:[
      {feature:'正确账号密码可登录',caseIDs:[ids[0]]},{feature:'错误提示文案正确',caseIDs:[ids[1]]},{feature:'不存在的用例',caseIDs:['NOT-EXIST']}],risks:[
      {title:'错误提示不正确',detail:'用例2失败，影响登录失败反馈',caseIDs:[ids[1],'NOT-EXIST']}]}})});
  });
  await page.reload();
  await page.waitForFunction(()=>typeof state!=='undefined'&&state&&state.folders);
  await page.evaluate(([vid])=>{summarizeFolderTestReport(vid,'auth')},[v.id]);
  // 进度：弹窗立即打开，前两步（整理用例、载入需求）已完成，等待 AI 时显示已等待时间。
  await page.locator('#folder-summary[open]').waitFor();
  assert.match(await page.locator('#fs-steps li').nth(0).innerText(),/5 条用例/);
  assert.match(await page.locator('#fs-steps li').nth(1).innerText(),/1 份需求/);
  assert.equal(await page.locator('#fs-steps li').nth(2).getAttribute('data-state'),'running');
  release();
  await page.locator('#fs-result').waitFor({state:'visible'});
  assert.equal(await page.locator('#fs-steps li').nth(3).getAttribute('data-state'),'done');
  const preview=await page.locator('#fs-preview').innerText();
  assert.match(preview,/整体可用，存在失败项/);
  const [file]=await Promise.all([page.waitForEvent('download'),page.click('#fs-download')]);
  const md=require('node:fs').readFileSync(await file.path(),'utf8');

  assert.deepEqual(prompt.stats,{total:5,passed:1,failed:1,blocked:0,notRun:3});
  assert.equal(prompt.requirements.length,1,'only the requirement matched by case-id prefix is sent');
  assert.match(prompt.requirements[0].content,/账号密码登录/);
  assert.ok(prompt.openIssues.some(i=>i.title===`登录偶发超时 ${CODE}`));
  assert.ok(!JSON.stringify(prompt).includes('base64'),'record images are stripped from the prompt');
  assert.match(md,/\| 5 \| 1 \| 1 \| 0 \| 3 \|/);
  // 一个目录一节、两列表；默认一用例一行，结果按所含用例汇总；AI 没覆盖的用例补一行，虚构的用例被丢弃；不列步骤。
  assert.match(md,new RegExp(`### 登录与认证\\n\\n关联需求：${doc.Title}\\n\\n\\| 功能点 \\| 结果 \\|\\n\\| --- \\| --- \\|\\n\\| 正确账号密码可登录 \\| 通过 \\|\\n\\| 错误提示文案正确 \\| 失败 \\|`));
  assert.match(md,/\| 用例3 \| 未执行 \|/);
  assert.ok(!md.includes('不存在的用例'));
  assert.ok(!md.includes('| s |'),'case steps are not listed');
  assert.match(md,/## 高风险项\n\n- \*\*错误提示不正确\*\*：用例2失败，影响登录失败反馈（TC-/);
  assert.ok(!md.includes('NOT-EXIST'),'case ids invented by the model are dropped');
  // 失败与取消：失败显示原因和重试；关闭弹窗会中止请求且不再改动界面。
  await page.keyboard.press('Escape');
  await page.unroute('**/api/general-agent/generate');
  await page.route('**/api/general-agent/generate',route=>route.fulfill({status:502,contentType:'application/json',body:JSON.stringify({error:{message:'boom'}})}));
  await page.evaluate(([vid])=>{summarizeFolderTestReport(vid,'auth')},[v.id]);
  await page.locator('#fs-error').waitFor({state:'visible'});
  assert.match(await page.locator('#fs-error').innerText(),/boom/);
  assert.equal(await page.locator('#fs-retry').isVisible(),true);
  assert.equal(await page.locator('#fs-steps li').nth(2).getAttribute('data-state'),'error');
  assert.deepEqual(errors,[]);
  await browser.close();
  console.log('folder summary ok');
})().catch(e=>{console.error(e);process.exit(1)});
