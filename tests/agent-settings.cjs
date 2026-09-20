// Only run against a temporary CASEHUB_STORE=memory server: every agent configuration this writes
// lives in that server's store, so nothing outside it is touched.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const base=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';
(async()=>{
 const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base);await page.locator('.folder-row').first().waitFor();
  const runtimeURL=base+'/api/agent-settings/playwright/runtime';
  const fixture=await (await page.request.get(runtimeURL)).json();
  assert.equal((await page.request.put(runtimeURL,{data:{revision:fixture.revision,content:JSON.stringify({model:'fixture-model',env:{CUSTOM:'keep-me'}})}})).status(),200);
  const open=async()=>{await page.locator('#avatar-menu').click();await page.locator('#context-menu button',{hasText:'设置'}).click();await page.locator('#agent-settings-form').waitFor()};
  const field=name=>page.locator(`#agent-settings-form [name="${name}"]`);
  const save=async()=>{await page.locator('#agent-settings-form [type="submit"]').click();await page.locator('#agent-settings-status').filter({hasText:'业务默认参数已保存'}).waitFor()};
  await open();
  assert.equal(await page.locator('#settings-agents [aria-current="true"]').innerText(),'aigc用例设计');
  for(const [name,value] of Object.entries({baseUrl:'https://default.test/login',instructions:'默认登录范围',testAccount:'default-user',testSecret:'default-password',timeoutMinutes:'25'}))await field(name).fill(value);
  await field('timeoutMinutes').fill('241');
  await page.locator('#agent-settings-form [type="submit"]').click();
  assert.equal(await field('timeoutMinutes').evaluate(el=>el.validity.valid),false);
  await field('timeoutMinutes').fill('25');await save();
  assert.equal(await field('testSecret').inputValue(),'default-password');
  assert.equal(await page.locator('.settings-restart-mark').count(),0);
  assert.match(await page.locator('#agent-runtime-apply-hint').innerText(),/无需重启/);
  const runtimeEditor=page.locator('#agent-runtime-json');
  const original=JSON.parse(await runtimeEditor.inputValue());
  assert.equal(original.model,'fixture-model');assert.equal(original.env.CUSTOM,'keep-me');
  original.model='configured-model';original.env.NEW_OPTION='new-value';original.custom={nested:[true,2,'preserved']};
  await runtimeEditor.fill(JSON.stringify(original,null,2));
  await page.locator('#agent-runtime-form [type="submit"]').click();
  await page.locator('#agent-runtime-status').filter({hasText:'配置已保存'}).waitFor();
  await runtimeEditor.fill('{bad json');await page.locator('#agent-runtime-form [type="submit"]').click();
  await page.locator('#agent-runtime-status').filter({hasText:'有效的 JSON'}).waitFor();
  await runtimeEditor.fill(JSON.stringify(original,null,2));
  await page.screenshot({path:'/tmp/casehub-agent-settings.png'});
  await field('baseUrl').fill('https://unsaved.test');await page.locator('#settings-close').click();
  await open();assert.equal(await field('baseUrl').inputValue(),'https://default.test/login');
  assert.deepEqual(JSON.parse(await runtimeEditor.inputValue()),original);
  await page.locator('#settings-close').click();
  // A separate browser context has no shared localStorage, but gets the same defaults.
  const other=await browser.newContext();const second=await other.newPage();await second.goto(base);
  await second.locator('#avatar-menu').click();await second.locator('#context-menu button',{hasText:'设置'}).click();await second.locator('#agent-settings-form').waitFor();
  assert.equal(await second.locator('#agent-settings-form [name="baseUrl"]').inputValue(),'https://default.test/login');
  assert.equal(await second.locator('#agent-settings-form [name="testSecret"]').inputValue(),'default-password');
  assert.equal(JSON.parse(await second.locator('#agent-runtime-json').inputValue()).model,'configured-model');
  await other.close();
  const id=await page.evaluate(async()=>{const title=`设置验证 ${Date.now()}`;await act('createReqDoc',{FolderID:'req-root',Title:title,Content:'登录'});return state.reqDocs.find(d=>d.Title===title).ID});
  await page.locator('[data-app="requirements"]').click();await page.locator(`[data-req-doc="${id}"]`).click();await page.locator('#req-ai-design').click();
  await page.locator('#ai-agent').selectOption('playwright');
  assert.equal(await page.locator('#ai-form [name="baseUrl"]').inputValue(),'https://default.test/login');
  assert.equal(await page.locator('#ai-form [name="timeoutMinutes"]').inputValue(),'25');
  assert.equal(await page.locator('#ai-form [name="testSecret"]').inputValue(),'default-password');
  await page.locator('#ai-form [name="baseUrl"]').fill('https://task-specific.test');
  await page.locator('#ai-drawer-close').click();await open();
  await field('baseUrl').fill('https://new-default.test');await save();
  await page.locator('#settings-close').click();await page.locator('#req-ai-design').click();await page.locator('#ai-form').waitFor();
  assert.equal(await page.locator('#ai-form [name="baseUrl"]').inputValue(),'https://task-specific.test');
  await page.locator('#ai-drawer-close').click();
  // Failed saves retain input and can be retried.
  await open();await field('testAccount').fill('retry-user');
  await page.route('**/api/agent-settings/playwright',route=>route.request().method()==='PUT'?route.fulfill({status:500,json:{error:'test save failure'}}):route.continue());
  await page.locator('#agent-settings-form [type="submit"]').click();await page.locator('#agent-settings-status').filter({hasText:'保存失败'}).waitFor();
  assert.equal(await field('testAccount').inputValue(),'retry-user');await page.unroute('**/api/agent-settings/playwright');await save();
  // A file changed from another device causes a conflict, retaining the unsaved editor.
  const endpoint=base+'/api/agent-settings/playwright/runtime';
  const snapshot=await (await page.request.get(endpoint)).json();
  await page.request.put(endpoint,{data:{content:'{"model":"another-device"}',revision:snapshot.revision}});
  await runtimeEditor.fill('{"model":"my-draft"}');await page.locator('#agent-runtime-form [type="submit"]').click();
  await page.locator('#agent-runtime-status').filter({hasText:'配置已被其他设备'}).waitFor();
  assert.equal(await runtimeEditor.inputValue(),'{"model":"my-draft"}');
  page.once('dialog',dialog=>dialog.accept());await page.locator('#agent-runtime-reload').click();
  await page.waitForFunction(()=>document.querySelector('#agent-runtime-json').value.includes('another-device'));
  await page.locator('#agent-settings-reset').click();
  assert.equal(await field('testSecret').inputValue(),'');assert.equal(await field('timeoutMinutes').inputValue(),'15');
  await save();
  const reset=await (await page.request.get(base+'/api/agent-settings/playwright')).json();assert.equal(reset.testSecret,'');assert.equal(reset.baseUrl,'');
  // 脚本生成 agent 有自己的一行配置：切过去是空的，保存后不影响用例设计那份。
  await page.locator('[data-settings-agent="generator"]').click();
  await page.locator('#agent-runtime-form').waitFor();
  assert.equal(await page.locator('#agent-runtime-json').inputValue(),'');
  assert.equal(await page.locator('#agent-settings-form [name="baseUrl"]').inputValue(),'');
  await page.locator('#agent-runtime-json').fill('{"model":"generator-model"}');
  await page.locator('#agent-runtime-form [type="submit"]').click();
  await page.locator('#agent-runtime-status').filter({hasText:'配置已保存'}).waitFor();
  const stored=await Promise.all(['playwright','generator'].map(async id=>(await (await page.request.get(`${base}/api/agent-settings/${id}/runtime`)).json())));
  assert.equal(JSON.parse(stored[1].content).model,'generator-model');
  assert.notEqual(stored[0].content,stored[1].content);
  await page.locator('[data-settings-agent="playwright"]').click();await page.locator('#agent-runtime-form').waitFor();
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.locator('#settings-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth),true);
  assert.deepEqual(errors,[]);console.log('agent 设置回归通过');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
