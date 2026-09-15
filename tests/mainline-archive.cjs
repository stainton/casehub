// Run only against a disposable CASEHUB_STORE=memory server.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 try {
  const page=await browser.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081');
  await page.locator('.folder-row').first().waitFor();
  const id=await page.evaluate(async()=>{
   await act('createVersion',{Name:'archive UI'});
   const v=state.versions.find(v=>v.name==='archive UI').id;
   await act('createCase',{VersionID:v,FolderID:'root',Title:'before archive'});
   const id=cases(v).find(c=>c.Title==='before archive').ID;
   await act('editCase',{VersionID:v,CaseID:id,Title:'after archive'});
   await act('submitRecord',{VersionID:v,CaseID:id,Result:'passed',Note:'**archived result**'});
   await act('mergeCases',{VersionID:v,CaseIDs:[id],TargetFolderID:'root'});
   setFocus({type:'case',versionID:'main',id});
   return {id,v};
  });
  await page.locator('#open-record').click();
  assert.equal(await page.locator('#record-version').inputValue(),'main');
  assert.equal(await page.locator('#save-draft').isVisible(),false);
  await page.locator('#record-version').selectOption(id.v);
  assert.equal(await page.locator('#save-draft').isVisible(),true);
  await page.locator('#drawer-close').click();
  await page.evaluate(async({v,id})=>{await act('deleteVersion',{VersionID:v});setFocus({type:'case',versionID:'main',id})},id);
  await page.locator('#open-record').click();
  assert.equal(await page.locator('.record').count(),1);
  assert.match(await page.locator('.record').innerText(),/来源 archive UI/);
  await page.locator('.record summary').click();
  assert.equal(await page.locator('.record-note strong').innerText(),'archived result');
  assert.equal(await page.locator('#save-draft').isVisible(),false);
  await page.locator('#drawer-close').click();
  await page.locator('#case-version-toggle').click();
  await page.locator('.history-row').filter({hasText:'编辑'}).click();
  assert.match(await page.locator('.history-snapshot').innerText(),/before archive/);
  assert.match(await page.locator('.history-snapshot').innerText(),/after archive/);
  await page.reload();
  await page.locator('.history-snapshot').waitFor();
  assert.match(await page.locator('.history-snapshot').innerText(),/before archive/);
  assert.deepEqual(errors,[]);
  console.log('PASS: archived mainline records, branch switching, deleted version, rich text, edit snapshots and reload');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
