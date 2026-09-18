// QA of the architecture deliverable, not of the future training application.
const {chromium} = require('/Users/lucifer/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');
const fs = require('fs');
const path = require('path');
const {pathToFileURL} = require('url');

(async () => {
  const root = path.resolve(__dirname, '..');
  const out = path.resolve(root, '../..', 'tmp/architecture/qa');
  fs.mkdirSync(out, {recursive:true});
  const browser = await chromium.launch({headless:true,executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
  const page = await browser.newPage({viewport:{width:1440,height:1050},deviceScaleFactor:1});
  const errors=[];
  page.on('pageerror', e=>errors.push(String(e)));
  const requests=[];
  page.on('request', r=>{ if(/^https?:/.test(r.url())) requests.push(r.url()); });
  await page.goto(pathToFileURL(path.join(root,'architecture-atlas.html')).href);
  const results=[];
  for (const id of ['contexts','code','flows','stop','data','deployment']) {
    await page.locator('#tab-'+id).click();
    await page.locator('#panel-'+id+' img').evaluate(img=>img.decode());
    if(await page.locator('[role=tabpanel]:visible').count()!==1) throw Error('Visible panel count: '+id);
    if(await page.locator('#tab-'+id).getAttribute('aria-selected')!=='true') throw Error('Selected tab: '+id);
    await page.screenshot({path:path.join(out,id+'.png'),fullPage:true});
    results.push({id,loaded:true});
  }
  await page.locator('#tab-contexts').click();
  await page.locator('#tab-contexts').focus();
  await page.keyboard.press('ArrowRight');
  if(await page.locator('#tab-code').getAttribute('aria-selected')!=='true') throw Error('Keyboard navigation');
  await page.setViewportSize({width:390,height:844});
  await page.locator('#tab-contexts').click();
  await page.screenshot({path:path.join(out,'mobile.png'),fullPage:true});
  const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth);
  const missingLinks=await page.locator('a[href]').evaluateAll(links=>links.map(a=>a.getAttribute('href')));
  for(const link of missingLinks) if(!fs.existsSync(path.resolve(root,link.split('#')[0]))) throw Error('Missing local link: '+link);
  if(errors.length||requests.length||overflow) throw Error(JSON.stringify({errors,requests,overflow}));
  const result={panels:results,jsErrors:errors,networkRequests:requests,pageOverflowAt390:overflow,keyboardNavigation:true,linksChecked:missingLinks.length};
  fs.writeFileSync(path.join(out,'checks.json'),JSON.stringify(result,null,2));
  console.log(JSON.stringify(result));
  await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
