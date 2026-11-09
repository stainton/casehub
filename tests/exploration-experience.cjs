// Run only against a temporary CASEHUB_STORE=memory server.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const base=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';

(async()=>{
  const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
  try{
    const page=await browser.newPage(),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    await page.goto(base);
    await page.waitForFunction(()=>state?.reqDocs?.length);
    await page.locator('#avatar-menu').click();
    await page.locator('#context-menu button',{hasText:'探索经验'}).click();
    const dialog=page.locator('#experience-dialog'),editor=dialog.locator('#experience-editor');
    await dialog.waitFor();
    const doc=await page.evaluate(()=>state.reqDocs[0]);
    await dialog.locator('#experience-search').fill(doc.ID);
    await dialog.locator(`[data-experience="doc:${doc.ID}"]`).click();
    await editor.fill('登录入口在 /login');
    await dialog.locator('#experience-form button[type="submit"]').click();
    await dialog.locator('#experience-status').filter({hasText:'已保存'}).waitFor();
    assert.equal(await page.evaluate(id=>state.reqDocs.find(x=>x.ID===id).ExplorationNotes,doc.ID),'登录入口在 /login');

    page.once('dialog',prompt=>prompt.accept('https://app.example.test/login'));
    await dialog.locator('#experience-add').click();
    assert.equal(await editor.inputValue(),'');
    await editor.fill('旧入口\n\n旧定位');
    await dialog.locator('#experience-form button[type="submit"]').click();
    await dialog.locator('#experience-status').filter({hasText:'已保存'}).waitFor();
    const origin='https://app.example.test';
    assert.equal(await page.evaluate(key=>state.productExperiences.find(x=>x.Origin===key).Notes,origin),'旧入口\n\n旧定位');
    await editor.fill('修正后的入口');
    await dialog.locator('#experience-form button[type="submit"]').click();
    await dialog.locator('#experience-status').filter({hasText:'已保存'}).waitFor();
    assert.equal(await page.evaluate(key=>state.productExperiences.find(x=>x.Origin===key).Notes,origin),'修正后的入口');
    await dialog.locator('#experience-close').click();
    await page.locator('#avatar-menu').click();
    await page.locator('#context-menu button',{hasText:'探索经验'}).click();
    await dialog.locator('#experience-search').fill(origin);
    await dialog.locator(`[data-experience="product:${origin}"]`).click();
    assert.equal(await editor.inputValue(),'修正后的入口');
    const update=await page.request.post(base+'/api/action',{data:{Type:'recordProductExploration',Origin:origin,ExplorationNotes:'新发现'}});
    assert.equal(update.status(),200);
    await editor.fill('过时的修改');
    await dialog.locator('#experience-form button[type="submit"]').click();
    await dialog.locator('#experience-status').filter({hasText:'重新读取后再保存'}).waitFor();
    page.once('dialog',confirmation=>confirmation.accept());
    await dialog.locator('#experience-reload').click();
    await page.waitForFunction(()=>document.querySelector('#experience-editor')?.value.includes('新发现'));
    await dialog.locator('#experience-close').click();
    await page.locator('[data-app="requirements"]').click();
    await page.evaluate(id=>setReqFocus({type:'doc',id}),doc.ID);
    await page.locator('#req-detail .link-button',{hasText:'探索记录'}).click();
    assert.equal(await editor.inputValue(),'登录入口在 /login');
    assert.deepEqual(errors,[]);
    console.log('探索经验编辑回归通过');
  }finally{await browser.close()}
})().catch(error=>{console.error(error);process.exit(1)});
