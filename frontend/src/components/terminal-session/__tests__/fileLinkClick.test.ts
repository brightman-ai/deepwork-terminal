import { describe, expect, it } from 'bun:test'
import { createFileLinkClickGate } from '../fileLinkClick'
const down={button:0,clientX:189,clientY:92,pointerType:'mouse'}
const up={button:0,clientX:189,clientY:92,detail:1}
describe('terminal file click recovery',()=>{
 it('recovers repeated same-cell clicks after the native hover was invalidated',()=>{
  const positions:number[][]=[];const gate=createFileLinkClickGate((x,y)=>{positions.push([x,y]);return true})
  gate.begin(down);gate.claim();expect(gate.complete(up)).toBe(false)
  gate.begin(down);expect(gate.complete(up)).toBe(true)
  gate.begin(down);expect(gate.complete(up)).toBe(true)
  expect(positions).toEqual([[189,92],[189,92]])
  expect(gate.complete(up)).toBe(false)
 })
 it('does not duplicate an activation handled by xterm',()=>{
  let hits=0;const gate=createFileLinkClickGate(()=>{hits++;return true})
  gate.begin(down);gate.claim();expect(gate.complete(up)).toBe(false);expect(hits).toBe(0)
 })
 it('leaves touch taps, drags, secondary buttons and double selection to their existing handlers',()=>{
  let hits=0;const gate=createFileLinkClickGate(()=>{hits++;return true})
  gate.begin({...down,pointerType:'touch'});expect(gate.complete(up)).toBe(false)
  gate.begin(down);expect(gate.complete({...up,clientX:210})).toBe(false)
  gate.begin({...down,button:2});expect(gate.complete({...up,button:2})).toBe(false)
  gate.begin(down);expect(gate.complete({...up,detail:2})).toBe(false)
  expect(hits).toBe(0)
 })
 it('does not invent a link when the live cell resolver finds no local file',()=>{
  const gate=createFileLinkClickGate(()=>false);gate.begin(down);expect(gate.complete(up)).toBe(false)
 })
})
