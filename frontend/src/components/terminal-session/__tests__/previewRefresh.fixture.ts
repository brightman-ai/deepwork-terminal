import assert from 'node:assert/strict'
import { plugin } from 'bun'
import { findTerminalFileLinks } from '../terminalFileLinks'
import { defineComponent, h, nextTick } from 'vue'
import { installVueSfcLoader } from '../../../portals/cli/__tests__/support/sfcLoader'
import { byTestId, findAll, fire, mountView, type TNode } from '../../../portals/cli/__tests__/support/testRenderer'
const events = new EventTarget()
const media = { matches:false, addEventListener(){},removeEventListener(){},addListener(){},removeListener(){} }
Object.assign(globalThis, {
 window: {location:{search:'',hash:'',pathname:'/'},innerWidth:1280,innerHeight:900,matchMedia:()=>media,addEventListener:events.addEventListener.bind(events),removeEventListener:events.removeEventListener.bind(events)},
 document:{visibilityState:'visible',documentElement:{clientWidth:1280,clientHeight:900},body:{offsetHeight:0},addEventListener(){},removeEventListener(){}},
 localStorage:{getItem(){return null},setItem(){}},
 ResizeObserver:class { observe(){} unobserve(){} disconnect(){} },
 requestAnimationFrame:(f:()=>void)=>setTimeout(f,0), cancelAnimationFrame:clearTimeout,
 getComputedStyle:()=>({transitionDelay:'0s',transitionDuration:'0s',animationDelay:'0s',animationDuration:'0s'}),
})
;(window as any).getComputedStyle = getComputedStyle
const searches:string[]=[]
const requests:{path:string;signal?:AbortSignal;cache?:string}[]=[]
let body='第一版';let status=200;let late:((response:Response)=>void)|null=null;let hold=false
const revoked:string[]=[];const originalRevoke=URL.revokeObjectURL;URL.revokeObjectURL=(url)=>revoked.push(url)
globalThis.fetch=(async(input:RequestInfo|URL,opts?:RequestInit)=>{
 const url=new URL(String(input),'http://fixture')
 if(url.pathname.endsWith('/files/raw')) {
  requests.push({path:url.searchParams.get('path')!,signal:opts?.signal??undefined,cache:opts?.cache})
  if(hold){hold=false;return new Promise<Response>(resolve=>{late=resolve})}
  if(url.searchParams.get('path')?.startsWith('missing/'))return Response.json({error:'not found'},{status:404})
  if(status!==200)return Response.json({error:'unavailable'},{status})
  if(url.searchParams.get('path')!.endsWith('.png'))return new Response(new Blob(['image'],{type:'image/png'}))
  return new Response(body,{headers:{'Content-Type':'text/plain'}})
 }
 if(url.pathname.endsWith('/files/search')) {
  const query=url.searchParams.get('q')!;searches.push(query)
  return Response.json({entries:query==='研判链范式.md'?[{name:'研判链范式.md',rel:'found/研判链范式.md',isDir:false,size:100,mtimeMs:0}]:[],truncated:false,indexState:'ready'})
 }
 if(url.pathname.endsWith('/files/tree'))return Response.json({cwd:'/project',rel:'.',entries:[]})
 return Response.json({entries:[],items:[],maxBytes:1000000})
}) as typeof fetch
plugin({name:'preview-renderer-stubs',setup(build){build.onResolve({filter:/(FilePreview|DocxPreview|PdfPreview|SheetPreview|AudioPreview|ArchivePreview|XmindPreview)\.vue$/},()=>({path:import.meta.dir+'/previewReaderStub.ts'}))}})
installVueSfcLoader()
const {default:FilesPanel}=await import('../FilesPanel.vue')
let panel:any
const wrapper=defineComponent({setup:()=>()=>h(FilesPanel,{ref:(v:any)=>{panel=v},sessionId:'session',cwd:'/project',mode:'tree',active:false})})
const view=mountView(wrapper,{})
const text=(n:TNode):string=>n.kind==='text'?n.text:n.children.map(text).join('')
const shown=()=>text(view.host)
const click=(id:string)=>{const node=byTestId(view.host,id)[0];assert(node,'missing '+id);fire(node,'onClick',{})}
async function wait(check:()=>boolean){const end=Date.now()+1500;while(Date.now()<end){await nextTick();if(check())return;await new Promise(r=>setTimeout(r,2))}throw Error('not reached: '+shown())}
try {
 await wait(()=>!!panel)
 panel.previewPath('docs/会更新.md');await wait(()=>shown().includes('第一版'))
 body='第二版';panel.previewPath('docs/会更新.md');await wait(()=>shown().includes('第二版'))
 assert.equal(requests.length,2,'a repeated terminal click must actually read again')
 assert.equal(byTestId(view.host,'fp-preview-title').length,1)
 fire(byTestId(view.host,'fixture-reader')[0],'onScroll',{});await nextTick()
 body='第三版';click('fp-preview-refresh');await wait(()=>shown().includes('第三版'))
 assert.equal(byTestId(view.host,'fixture-reader')[0].props['data-scroll'],140)
 body='旁证';panel.previewPath('docs/旁证.md');await wait(()=>shown().includes('旁证'))
 const readsBeforeTabSwitch=requests.length
 const cachedTab=findAll(view.host,n=>n.tag==='button' && n.props.title==='docs/会更新.md')[0];assert(cachedTab);fire(cachedTab,'onClick',{})
 await wait(()=>shown().includes('第三版'));assert.equal(requests.length,readsBeforeTabSwitch,'document tab selection must keep its reading snapshot')
 assert.equal(byTestId(view.host,'fixture-reader')[0].props['data-scroll'],140)
 status=500;click('fp-preview-refresh');await wait(()=>shown().includes('刷新失败'))
 assert(shown().includes('第三版'),'refresh failure must retain known content')
 status=200;body='第四版';click('fp-preview-refresh-retry');await wait(()=>shown().includes('第四版'))
 hold=true;panel.previewPath('docs/会更新.md');await wait(()=>!!late)
 assert(shown().includes('第四版'));assert(byTestId(view.host,'fp-preview-refreshing').length)
 const old=late!;late=null;const oldRequest=requests.at(-1)!
 body='第五版';panel.previewPath('docs/会更新.md');await wait(()=>shown().includes('第五版'))
 assert(oldRequest.signal?.aborted);old(new Response('迟到旧稿'));await new Promise(r=>setTimeout(r,10));assert(!shown().includes('迟到旧稿'))
 panel.previewPath('docs/会更新.md',3,5);await wait(()=>byTestId(view.host,'fixture-reader')[0]?.props['data-line']===3)
 hold=true;panel.previewPath('docs/会更新.md');await wait(()=>!!late)
 const closed=late!;late=null;click('fp-preview-close');closed(new Response('关闭后旧稿'));await new Promise(r=>setTimeout(r,10))
 assert(!shown().includes('关闭后旧稿'));if(byTestId(view.host,'fp-preview').length)click('fp-preview-close')
 await wait(()=>byTestId(view.host,'fp-preview').length===0)
 panel.previewPath('docs/image.png');await wait(()=>findAll(view.host,n=>n.tag==='img').length===1)
 click('fp-preview-refresh');await wait(()=>revoked.length===1)
 click('fp-preview-close');assert.equal(revoked.length,2,'replaced and closed image URLs must both be released')
 const link=findTerminalFileLinks('● Read(missing/研判链范式.md ·lines 240-539)')[0]!
 body='行范围正文';panel.previewPath(link.path,link.lineStart,link.lineEnd)
 await wait(()=>byTestId(view.host,'fp-path-candidate-found/研判链范式.md').length===1)
 assert.equal(byTestId(view.host,'fp-path-resolution-reference')[0].children.map(text).join(''),'定位文件：研判链范式.md')
 assert.deepEqual(searches,['missing/研判链范式.md','研判链范式.md'])
 click('fp-path-candidate-found/研判链范式.md');await wait(()=>shown().includes('行范围正文'))
 assert.equal(byTestId(view.host,'fixture-reader')[0].props['data-line'],240)
 assert(requests.every(r=>r.cache==='no-store'),'explicit reads must bypass HTTP cache')
 console.log(JSON.stringify({passed:true,repeated_path_reads:true,refresh_and_retry:true,scroll_preserved:true,tab_snapshot_preserved:true,late_and_closed_isolation:true,line_navigation:true,image_cleanup:true,cache_bypass:true,annotation_search_and_line:true}))
}finally{view.unmount();URL.revokeObjectURL=originalRevoke}
