import { expect, it } from 'bun:test'
import { overviewPaneTarget, selectOverviewCard } from '../overviewSelection'
const tabs=[{id:'split',sessionId:'first',split:{secondSessionId:'second'}},{id:'normal',sessionId:'third'},{id:'ended'}]
it('resolves sessions to the owning tab and pane after a split adds an extra card',()=>{
 // The old card index 3 has no third visible tab, even though session third exists.
 const switched:string[]=[];selectOverviewCard(3,['split','normal'],{switchTo:id=>switched.push(id),closeOverview(){}})
 expect(switched).toEqual([])
 expect(overviewPaneTarget('first',tabs)).toEqual({tabId:'split',pane:'first'})
 expect(overviewPaneTarget('second',tabs)).toEqual({tabId:'split',pane:'second'})
 expect(overviewPaneTarget('third',tabs)).toEqual({tabId:'normal',pane:'first'})
})
it('does not retarget a stale card after reordering or closure',()=>{
 expect(overviewPaneTarget('third',[tabs[1],tabs[0]])).toEqual({tabId:'normal',pane:'first'})
 expect(overviewPaneTarget('second',[tabs[1]])).toBeUndefined()
 expect(overviewPaneTarget('tab:ended',tabs)).toEqual({tabId:'ended',pane:'first'})
 expect(overviewPaneTarget('',tabs)).toBeUndefined()
})
