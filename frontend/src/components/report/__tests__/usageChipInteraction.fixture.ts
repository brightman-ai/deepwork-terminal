// Isolated subprocess: real SFC + Vue handlers + composable, synthetic transport.
// Keep browser globals/fetch out of the rest of Bun's shared test process.
import assert from 'node:assert/strict'
import { installVueSfcLoader } from '../../../portals/cli/__tests__/support/sfcLoader'
import { findAll, fire, mountView, type TNode } from '../../../portals/cli/__tests__/support/testRenderer'
import { nextTick } from 'vue'

const events = new EventTarget()
Object.assign(globalThis, {
  window: {location: {search:'',hash:'',pathname:'/'},history:{replaceState(){}},innerWidth:1280,innerHeight:900,requestAnimationFrame:()=>1,cancelAnimationFrame(){},addEventListener:events.addEventListener.bind(events),removeEventListener:events.removeEventListener.bind(events)},
  document: {visibilityState:'visible',documentElement:{clientWidth:1280,clientHeight:900},addEventListener:events.addEventListener.bind(events),removeEventListener:events.removeEventListener.bind(events)},
  localStorage: {getItem(){return null},setItem(){}},
  requestAnimationFrame: () => 1, cancelAnimationFrame() {},
  ResizeObserver: class {observe(){} disconnect(){}},
})
const requests: string[] = []
const counts = new Map<string,number>()
let finishLate!: (response: Response) => void
function report(window: string) {
  const days = window === '7d' ? 7 : window === '14d' ? 14 : window === '30d' ? 30 : 1
  const row = {vendor:'zhipu',vendor_display:'GLM Coding Plan',runtime:'claude',runtime_provider:'glm',billing_mode:'subscription',billing_coverage:'complete',input_tokens:days*10000,output_tokens:days*100,cache_read_tokens:0,total_tokens:days*10100,cost:days*1.01,currency:'USD',requests:days,priced_requests:days}
  return {window,available:true,calculation_state:'fresh',providers:[row],rows:[],summary:row}
}
globalThis.fetch = (async (input: RequestInfo | URL) => {
  const url = new URL(String(input),'http://fixture')
  if(url.pathname.endsWith('/usage/report')) {
    const window=url.searchParams.get('window')!;requests.push(window);counts.set(window,(counts.get(window)??0)+1)
    if(window==='7d' && counts.get(window)===1)return Response.json({window,available:false,reason:'calculating',calculation_state:'calculating'})
    if(window==='14d' && counts.get(window)===1)return Response.json({}, {status:500})
    if(window==='30d')return new Promise<Response>(resolve=>{finishLate=resolve})
    return Response.json(report(window))
  }
  if(url.pathname.endsWith('/usage/quota'))return Response.json({quotas:[{runtime:'claude',vendor:'zhipu',display:'GLM Coding Plan',present:true,billing:'subscription',health:{state:'healthy'},windows:[{kind:'7d',remaining_percent:28,used_percent:72,window_minutes:10080}],snapshot:{age_seconds:0,stale:false}}]})
  return Response.json({runtime_profiles:[]})
}) as typeof fetch

installVueSfcLoader()
const {default:UsageChip}=await import('../UsageChip.vue')
const view=mountView(UsageChip,{})
const text=(n:TNode):string=>n.kind==='text'?n.text:n.children.map(text).join('')
const buttons=()=>[...findAll(view.host,n=>n.tag==='button'),...findAll(view.teleports,n=>n.tag==='button')]
const click=(label:string)=>{const b=buttons().find(n=>text(n).trim()===label);assert(b,'missing '+label);fire(b,'onClick',{stopPropagation(){}})}
const body=()=>text(view.teleports)
async function wait(predicate:()=>boolean){const until=Date.now()+2000;while(Date.now()<until){await nextTick();if(predicate())return;await new Promise(r=>setTimeout(r,5))}throw Error('component did not reach expected state: '+body())}
try {
  await wait(()=>requests.includes('24h'))
  const entry=findAll(view.host,n=>n.tag==='button' && String(n.props.class).includes('uchip'))[0]!
  fire(entry,'onClick',{stopPropagation(){}});await nextTick()
  click('7天');await wait(()=>body().includes('正在计算'));await wait(()=>body().includes('7.07'))
  assert.equal(counts.get('7d'),2,'window selection must request and follow cold calculation')
  click('14天');await wait(()=>body().includes('500'));click('重试');await wait(()=>body().includes('14.14'))
  click('30天');await wait(()=>!!finishLate);click('7天');await wait(()=>body().includes('7.07'))
  finishLate(Response.json(report('30d')));await new Promise(r=>setTimeout(r,5));await nextTick()
  assert(body().includes('7.07'));assert(!body().includes('30.30'))
  const chosen=buttons().find(n=>n.props['aria-selected']===true && text(n)==='7天');assert(chosen)
  const escape=new Event('keydown',{cancelable:true});Object.defineProperty(escape,'key',{value:'Escape'});events.dispatchEvent(escape);await nextTick()
  assert(escape.defaultPrevented);assert(!body().includes('用量 /'))
  console.log(JSON.stringify({passed:true,selection_requests:requests,cold_recovery:true,error_retry:true,late_window_isolation:true,escape_closes:true}))
} finally {view.unmount()}
