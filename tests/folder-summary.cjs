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

  let prompt;
  await page.route('**/api/general-agent/generate',route=>{
    prompt=JSON.parse(JSON.parse(route.request().postData()).prompt);
    route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({output:{conclusion:'整体可用，存在失败项。',risks:[
      {level:'high',title:'错误提示不正确',detail:'用例2失败',caseIDs:[ids[1],'NOT-EXIST'],requirement:doc.Title,suggestion:'修复后回归'}]}})});
  });
  await page.reload();
  await page.waitForFunction(()=>typeof state!=='undefined'&&state&&state.folders);
  const download=page.waitForEvent('download');
  await page.evaluate(([vid])=>summarizeFolderTestReport(vid,'auth'),[v.id]);
  const file=await download;
  const md=require('node:fs').readFileSync(await file.path(),'utf8');

  assert.deepEqual(prompt.stats,{total:5,passed:1,failed:1,blocked:0,notRun:3});
  assert.equal(prompt.requirements.length,1,'only the requirement matched by case-id prefix is sent');
  assert.match(prompt.requirements[0].content,/账号密码登录/);
  assert.ok(prompt.openIssues.some(i=>i.title===`登录偶发超时 ${CODE}`));
  assert.ok(!JSON.stringify(prompt).includes('base64'),'record images are stripped from the prompt');
  assert.match(md,/\| 5 \| 1 \| 1 \| 0 \| 3 \|/);
  assert.match(md,new RegExp(`\\| ${ids[1]} \\| 用例2 .*\\| 失败 \\|`));
  assert.match(md,/\| FREE-3 .*未关联需求 \| 未执行 \|/);
  assert.match(md,/\[高\] 错误提示不正确/);
  assert.ok(!md.includes('NOT-EXIST'),'case ids invented by the model are dropped');
  assert.deepEqual(errors,[]);
  await browser.close();
  console.log('folder summary ok');
})().catch(e=>{console.error(e);process.exit(1)});
