// 「完成」/「归档版本」浏览器回归：点「完成」把测试版本收进用例树和测试任务底部的「归档版本」（默认折叠），完成后只读，可「重新打开」。
// 只对临时 CASEHUB_STORE=memory 服务运行。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const BASE=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';
(async()=>{
  const browser=await chromium.launch(),page=await browser.newPage({viewport:{width:1500,height:900}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  page.on('dialog',d=>d.accept());
  const NAME=`归档验证 ${Date.now()}`;
  await page.goto(BASE);
  await page.waitForFunction(()=>typeof state!=='undefined'&&state&&state.folders);
  await page.evaluate(()=>{window.__x=1});
  const act=(type,data)=>page.evaluate(([t,d])=>act(t,d),[type,data]);
  await act('createVersion',{Name:NAME});
  const vid=await page.evaluate(n=>state.versions.find(v=>v.name===n).id,NAME);
  await act('createTask',{VersionID:vid,Name:'归档任务',CaseIDs:['CASE-0001']});
  const version=page.locator(`.version[data-version="${vid}"]`);
  assert.equal(await page.locator('#versions .archive-group').count(),0,'no archive group before anything is archived');
  assert.ok(await version.locator('.case-check').count()>0,'an active version has selection checkboxes');

  await version.locator('[data-archive]').click();
  await page.locator('#versions .archive-group').waitFor();
  assert.match(await page.locator('#versions .archive-title').innerText(),/归档版本\s*1/);
  assert.equal(await page.locator(`#versions > .version[data-version="${vid}"]`).count(),0,'archived version left the main list');
  const archived=page.locator(`#versions .archive-body .version[data-version="${vid}"]`);
  assert.match(await archived.locator('.badge').innerText(),/已完成/);
  assert.equal(await archived.locator('.case-check').count(),0,'archived version is read-only: no checkboxes');
  assert.equal(await archived.locator('[data-sync],[data-merge],[data-archive]').count(),0,'no pull/merge/archive on an archived version');
  assert.equal(await archived.locator('[data-unarchive]').count(),1);

  // 测试任务里同样归入「归档版本」
  await page.click('[data-view="tasks"]');
  await page.locator('#tasks .archive-group').waitFor();
  assert.ok(await page.locator('#tasks .archive-body').innerText().then(t=>t.includes(NAME)&&t.includes('归档任务')));

  // 取消归档
  await page.click('[data-view="cases"]');
  await archived.locator('[data-unarchive]').click();
  await page.waitForFunction(()=>!document.querySelector('#versions .archive-group'));
  assert.ok(await page.locator(`#versions > .version[data-version="${vid}"] .case-check`).count()>0);
  assert.deepEqual(errors,[]);
  await browser.close();console.log('archive ok');
})().catch(e=>{console.error(e);process.exit(1)});
