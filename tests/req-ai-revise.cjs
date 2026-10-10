// 「AI 修正需求」浏览器回归：详情页按钮 + 右键菜单、变更确认、需求与关联用例一起保存、用例打标与确认。
// 通用 AI 服务用 page.route 拦截成固定结果（不调用模型）。只对临时 CASEHUB_STORE=memory 服务运行。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const BASE=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';
const CODE=`Q${Date.now().toString(36).toUpperCase().slice(-6)}`;

(async()=>{
  const browser=await chromium.launch(),page=await browser.newPage({viewport:{width:1500,height:900}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const action=body=>page.evaluate(async b=>(await fetch('/api/action',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b)})).json(),body);
  await page.goto(BASE);
  const st=await page.evaluate(async()=>(await fetch('/api/state')).json());
  const doc=st.reqDocs[0];
  await action({Type:'setReqDocCode',DocID:doc.ID,Code:CODE});
  await action({Type:'createPendingFolder',Name:'模块',Code:`${CODE}-MOD-FUNC`,ParentID:'pending-root'});
  const folder=(await page.evaluate(async()=>(await fetch('/api/state')).json())).pendingFolders.find(f=>f.Code===`${CODE}-MOD-FUNC`);
  await action({Type:'createPendingCase',FolderID:folder.ID,Title:'超长内容',Steps:'1. 输入 121 字符',Expected:'1. 超过 120 报错',Priority:'P1'});
  const caseID=`TC-${CODE}-MOD-FUNC-001`;

  let prompt;
  await page.route('**/api/general-agent/generate',route=>{
    prompt=JSON.parse(JSON.parse(route.request().postData()).prompt);
    route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({output:{
      requirement:(prompt.requirement||'')+'\n\n内容上限改为 200 字符。',summary:'上限 120→200',
      cases:[{ref:`p:${caseID}`,preconditions:'',steps:'1. 输入 201 字符',expected:'1. 超过 200 报错',reason:'长度上限改为 200'}]}})});
  });

  await page.reload();
  await page.click('[data-app="requirements"]');
  // 目录树默认折叠：展开需求树里所有目录再点文档。
  await page.evaluate(()=>document.querySelectorAll('#req-tree .folder-row.closed').forEach(r=>r.classList.remove('closed')));
  await page.click(`[data-req-doc="${doc.ID}"]`);
  // 详情页入口
  assert.equal(await page.locator('#req-ai-revise').innerText(),'AI 修正需求');
  // 右键菜单入口
  await page.locator(`[data-req-doc="${doc.ID}"]`).click({button:'right'});
  assert.ok(await page.locator('#context-menu button',{hasText:'AI 修正需求'}).count(),'right-click menu has AI 修正需求');
  await page.keyboard.press('Escape');await page.mouse.click(5,5);

  await page.click('#req-ai-revise');
  await page.fill('#modal-body textarea[name="points"]','内容上限改为 200');
  await page.click('#modal-form button[type="submit"]');
  await page.waitForSelector('#modal-title:has-text("确认 AI 修正需求")');
  assert.deepEqual(prompt.cases.map(c=>c.ref),[`p:${caseID}`],'only the related case is sent');
  assert.ok(await page.locator('.ai-revise-case',{hasText:caseID}).count(),'confirm dialog lists the affected case');
  await page.click('#modal-form button[type="submit"]');
  await page.waitForFunction(()=>!document.querySelector('#modal').open);

  const after=await page.evaluate(async()=>(await fetch('/api/state')).json());
  assert.ok(after.reqDocs.find(d=>d.ID===doc.ID).Content.includes('200 字符'),'requirement saved');
  const pc=after.pendingCases.find(c=>c.ID===caseID);
  assert.equal(pc.Steps,'1. 输入 201 字符');assert.equal(pc.AIRevisedReason,'长度上限改为 200');

  // 展示提醒：评审树里打标 + 详情页横幅 → 确认后清除
  await page.click('[data-reqview="review"]');
  await page.evaluate(()=>document.querySelectorAll('#review-tree .folder-row.closed').forEach(r=>r.classList.remove('closed')));
  await page.waitForSelector(`[data-review-case="${caseID}"] .ai-revised-tag`);
  await page.click(`[data-review-case="${caseID}"]`);
  await page.waitForSelector('.ai-revised-banner');
  assert.ok((await page.locator('.ai-revised-banner').innerText()).includes('长度上限改为 200'));
  await page.click('[data-ack-revision]');
  await page.waitForSelector('.ai-revised-banner',{state:'detached'});
  assert.equal(await page.locator(`[data-review-case="${caseID}"] .ai-revised-tag`).count(),0);
  assert.deepEqual(errors,[]);
  await browser.close();
  console.log('req-ai-revise ok');
})().catch(e=>{console.error(e);process.exit(1)});
