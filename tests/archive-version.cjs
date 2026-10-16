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
  assert.ok(!(await page.locator('#versions .version[data-version="main"] > .version-body').evaluate(b=>b.lastChild.nodeType===3&&b.lastChild.textContent.trim())),'no stray text (e.g. a 0) after the mainline tree');
  const version=page.locator(`.version[data-version="${vid}"]`);
  assert.ok(await version.locator('.case-check').count()>0,'an active version has selection checkboxes');
  assert.equal(await page.locator('[data-view="archive"]').innerText(),'归档版本','archive is a tab beside 用例树 / 测试任务');

  await version.locator('[data-archive]').click();
  await page.waitForFunction(id=>!document.querySelector(`#versions .version[data-version="${id}"]`),vid);
  await page.click('[data-view="tasks"]');
  assert.ok(!(await page.locator('#tasks').innerText()).includes(NAME),'a finished version leaves the 测试任务 list');

  await page.click('[data-view="archive"]');
  assert.equal(await page.locator('#archive-view').isVisible(),true);
  assert.equal(await page.locator('#case-view').isVisible(),false);
  const archived=page.locator(`#archive .version[data-version="${vid}"]`);
  assert.match(await archived.locator('.badge').innerText(),/已完成/);
  assert.equal(await archived.locator('.case-check').count(),0,'archived version is read-only: no checkboxes');
  assert.equal(await archived.locator('[data-sync],[data-merge],[data-archive]').count(),0,'no pull/merge/finish on an archived version');
  assert.equal(await archived.locator('[data-unarchive]').count(),1);
  assert.match(await archived.textContent(),/归档任务/,'its tasks are listed under the version');

  await archived.locator('[data-unarchive]').click();
  await page.waitForFunction(id=>!document.querySelector(`#archive .version[data-version="${id}"]`),vid);
  assert.match(await page.locator('#archive').innerText(),/还没有已完成/);
  await page.click('[data-view="cases"]');
  assert.ok(await page.locator(`#versions > .version[data-version="${vid}"] .case-check`).count()>0,'reopened version is back in 用例树');
  assert.deepEqual(errors,[]);
  await browser.close();console.log('archive ok');
})().catch(e=>{console.error(e);process.exit(1)});
