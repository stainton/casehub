// Run against a disposable CASEHUB_STORE=memory server. Healer responses are mocked; database writes are real.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({headless:true});
 try {
  const page=await browser.newPage(), errors=[];page.on('pageerror',e=>errors.push(e.message));
  const base=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18092';
  const action=async data=>{const r=await page.request.post(base+'/api/action',{data});assert.equal(r.status(),200,await r.text());return (await r.json()).state};
  let state=await action({Type:'createVersion',Name:'healer UI'}),vid=state.versions.find(v=>v.name==='healer UI').id;
  state=await action({Type:'saveScript',VersionID:vid,CaseID:'CASE-0001',ScriptCode:'original source',ScriptSummary:'original'});
  const script=state.scripts.find(s=>s.VersionID===vid);
  const jobID='11111111-1111-4111-8111-111111111111';let submitted,finish;
  const completion=new Promise(r=>{finish=r});
  await page.route('**/api/healer/status',route=>route.fulfill({json:{enabled:true}}));
  await page.route('**/api/generator/status',route=>route.fulfill({json:{enabled:false}}));
  await page.route('**/api/healer/jobs',route=>{submitted=route.request().postDataJSON();return route.fulfill({json:{id:jobID,status:'running',stage:'verifying'}})});
  await page.route(`**/api/healer/jobs/${jobID}/events`,async route=>{await completion;await route.fulfill({contentType:'text/event-stream',body:`event: snapshot\ndata: ${JSON.stringify({id:jobID,status:'succeeded',stage:'completed'})}\n\n`})});
  await page.route(`**/api/healer/jobs/${jobID}/result`,route=>route.fulfill({json:{generated:1,blocked:0,scripts:[{caseId:'CASE-0001',code:'repaired source',status:'generated',summary:'修复定位',fileName:'CASE-0001.spec.ts'}]}}));
  await page.goto(base);await page.locator('#versions .version').first().waitFor();
  assert.equal(await page.evaluate(()=>caseMenu(state.versions[0].id,'CASE-0001').some(x=>x[0].includes('修复'))),false);
  await page.locator('[data-app="automation"]').click();
  await page.evaluate(({vid})=>setAutoFocus({type:'script',versionID:vid,id:'CASE-0001'}),{vid});
  await page.locator('#script-heal').click();await page.locator('#gen-form').waitFor();
  await page.locator('#gen-form [name="baseUrl"]').fill('http://example.test');
  await page.locator('#gen-form [name="failureDetails"]').fill('locator missing');
  await page.locator('#gen-form button[type="submit"]').click();
  await page.waitForFunction(()=>document.querySelector('#script-heal').textContent.includes('修复中'));
  assert.equal(submitted.cases[0].script,'original source');assert.equal(submitted.cases[0].failureDetails,'locator missing');
  await page.locator('#gen-drawer-close').click();await page.locator('#script-heal').click();
  assert.equal(await page.locator('#gen-drawer').isVisible(),true);
  finish();
  await page.waitForFunction(({vid})=>scriptFor(vid,'CASE-0001')?.Code==='repaired source',{vid});
  const healed=(await(await page.request.get(base+'/api/state')).json()).scripts.find(s=>s.ID===script.ID);
  assert.equal(healed.Code,'repaired source');assert.equal(healed.JobID,jobID);
  // A blocked repair must leave this successfully saved script intact.
  await page.route('**/api/healer/jobs/22222222-2222-4222-8222-222222222222/result',route=>route.fulfill({json:{generated:0,blocked:1,scripts:[{caseId:'CASE-0001',status:'blocked',code:'',summary:'产品缺陷',missingInputs:['产品错误，不能修改预期绕过']}]}}));
  await page.evaluate(async({vid})=>{
    const t={kind:'healer',jobId:'22222222-2222-4222-8222-222222222222',versionID:vid,caseIDs:['CASE-0001'],label:'blocked',status:'succeeded',requirementIDs:[]};genTasks.unshift(t);await importGenResult(t);
  },{vid});
  assert.equal(await page.evaluate(({vid})=>scriptFor(vid,'CASE-0001').Code,{vid}),'repaired source');
  assert.deepEqual(errors,[]);
  console.log('PASS: automation-only heal entry, existing source/failure payload, clickable progress, successful save and blocked repair preserving original');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
