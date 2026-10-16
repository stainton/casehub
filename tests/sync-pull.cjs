// 「拉取主线」浏览器回归：弹窗列出可拉取的用例，由人勾选；不勾选不能提交，新增用例默认不勾，拉过的不再出现。
// 只对临时 CASEHUB_STORE=memory 服务运行。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{const b=await chromium.launch(),p=await b.newPage({viewport:{width:1500,height:900}});
const errs=[];p.on('pageerror',e=>errs.push(e.message));
await p.goto((process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081'));await p.waitForFunction(()=>typeof state!=='undefined'&&state&&state.folders);
const counts=()=>p.evaluate(async()=>{const st=await (await fetch('/api/state')).json();const v=st.versions.at(-1);return {cases:st.cases.filter(c=>c.VersionID===v.id).map(c=>c.ID),folders:st.folders.filter(f=>f.VersionID===v.id).length}});
await p.click('#create-version');await p.fill('[name="Name"]','空');await p.click('#modal-form button[type="submit"]');await p.locator('#modal').waitFor({state:'hidden'});
await p.locator('[data-sync]').last().click();
await p.locator('#modal[open] .sync-row').first().waitFor();
assert.equal(await p.locator('.sync-row').count(),2);
assert.equal(await p.locator('.sync-row input:checked').count(),0,'new cases are not pre-checked');
assert.deepEqual((await counts()).cases,[],'opening the dialog pulls nothing');
await p.click('#modal-form button[type="submit"]');await p.waitForTimeout(300);
assert.equal(await p.locator('#modal').evaluate(m=>m.open),true,'empty choice keeps the dialog open');
await p.locator('.sync-row input').first().check();
await p.click('#modal-form button[type="submit"]');await p.locator('#modal').waitFor({state:'hidden'});

await p.locator('[data-sync]').last().click();await p.locator('#modal[open] .sync-row').first().waitFor();
assert.equal(await p.locator('.sync-row').count(),1,'already-pulled case is no longer offered');
assert.deepEqual(errs,[]);await b.close();console.log('ok')})().catch(e=>{console.error(e);process.exit(1)})
