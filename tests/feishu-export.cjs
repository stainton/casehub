// Run against a disposable CASEHUB_STORE=memory server.
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{
  const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
  try{
    const context=await browser.newContext({permissions:['clipboard-read','clipboard-write']});
    const page=await context.newPage(),errors=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.goto(process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081');
    await page.locator('.folder-row').first().waitFor();
    const fixture=await page.evaluate(()=>{
      const versions=[{id:'a',name:'版本 A'},{id:'b',name:'版本 B'}];
      const folders=[{VersionID:'a',ID:'root',Name:'根目录'},{VersionID:'a',ID:'sub',ParentID:'root',Name:'登录'},
        {VersionID:'a',ID:'other',ParentID:'root',Name:'不应导出的目录'},{VersionID:'b',ID:'root',Name:'B 根目录'}];
      const special=`中文 🧪 <img src=x onerror=alert(1)> & "单引号'"`;
      const cases=[{VersionID:'a',ID:'1',FolderID:'sub',Title:special,Priority:'P0',Preconditions:'账号已创建',Steps:'1. 登录\n2. 退出',Expected:'首页\n登录页'},
        {VersionID:'a',ID:'2',FolderID:'other',Title:'未选择用例'},{VersionID:'b',ID:'1',FolderID:'root',Title:'B 用例'}];
      const out=buildFeishuMindmap(versions,folders,cases,[{versionID:'a',ids:['1']},{versionID:'b',ids:['1']}]);
      const dom=new DOMParser().parseFromString(out.html,'text/html');
      const payload=JSON.parse(decodeURIComponent(dom.querySelector('.mm-editor-clipboard').getAttribute('data-json')));
      const nodes=[];function walk(n){nodes.push(n);n.children.forEach(walk)}payload.data.nodes.forEach(walk);
      const folder=buildFeishuMindmap(versions,folders,cases,[{versionID:'a',folderID:'sub'}]);
      const empty=buildFeishuMindmap(versions,folders,[],[{versionID:'a',folderID:'root'}]);
      return {out,payload,nodes,special,hasImage:!!dom.querySelector('img'),folder,empty};
    });
    assert.equal(fixture.out.count,2);
    assert.equal(fixture.payload.type,'define');
    assert.equal(fixture.payload.data.structure,'right');
    assert.equal(fixture.payload.data.nodes[0].children.length,2);
    assert.equal(new Set(fixture.nodes.map(n=>n.id)).size,fixture.nodes.length);
    assert.ok(fixture.nodes.some(n=>n.text[0].text===`TC：${fixture.special}`));
    assert.ok(fixture.nodes.some(n=>n.text[0].text==='1. 登录\n2. 退出'));
    assert.doesNotMatch(fixture.out.text,/未选择用例|不应导出的目录/);
    assert.equal(fixture.hasImage,false);
    assert.equal(fixture.folder.count,1);
    assert.equal(fixture.empty.count,0);
    // Read actual browser clipboard and use the same DOM/URI/JSON decoding as Feishu.
    await page.locator('.folder-row').first().click({button:'right'});
    await page.getByRole('button',{name:'复制飞书思维导图',exact:true}).last().click();
    await page.locator('#feishu-export-copy').click();
    assert.match(await page.locator('#feishu-export-status').innerText(),/已复制/);
    const copied=await page.evaluate(async()=>{
      const items=await navigator.clipboard.read();
      const html=await (await items[0].getType('text/html')).text();
      const plain=await (await items[0].getType('text/plain')).text();
      const dom=new DOMParser().parseFromString(html,'text/html');
      return {plain,payload:JSON.parse(decodeURIComponent(dom.querySelector('.mm-editor-clipboard').getAttribute('data-json')))};
    });
    assert.equal(copied.payload.type,'define');
    assert.match(copied.plain,/TC：/);
    await page.locator('#feishu-export-close').click();
    // Bulk action collects all selected versions, even when case IDs overlap.
    await page.evaluate(()=>{selected=new Map([['main',new Set([cases('main')[0].ID])]]);updateBulk()});
    await page.locator('[data-bulk="feishu"]').click();
    assert.match(await page.locator('#feishu-export-status').innerText(),/共 1 条用例/);
    // Clipboard rejection stays visible and allows retry, with no false success.
    await page.evaluate(()=>{document.execCommand=()=>false;Object.defineProperty(navigator.clipboard,'write',{configurable:true,value:async()=>{throw Error('denied')}})});
    await page.locator('#feishu-export-copy').click();
    assert.match(await page.locator('#feishu-export-status').innerText(),/复制失败/);
    assert.equal(await page.locator('#feishu-export-copy').isEnabled(),true);
    assert.deepEqual(errors,[]);
    console.log('PASS: hierarchy, selection, Unicode/HTML safety, multiline fields, real rich clipboard, folder/bulk UI, rejection');
  }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
