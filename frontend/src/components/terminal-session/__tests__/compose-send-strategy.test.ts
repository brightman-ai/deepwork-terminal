import {describe,test,expect} from 'bun:test'
import {useComposeSendStrategy} from '../../../composables/cli/useComposeSendStrategy'
const {encode}=useComposeSendStrategy()
const decoder=new TextDecoder()
describe('assembled text uses one paste transaction',()=>{
 for(const text of ['ls -la','中文短句😀','line1\nline2','a'.repeat(201)]){
  test(`one frame ${text.length} chars`,()=>{
   const chunks=encode(text,true)
   expect(chunks.length).toBe(1)
   expect(decoder.decode(chunks[0])).toBe('\x1b[200~'+text+'\x1b[201~')
  })
 }
 test('plain shell gets one raw frame without unsupported escapes',()=>{
  const chunks=encode('echo hello',false)
  expect(chunks.length).toBe(1)
  expect(decoder.decode(chunks[0])).toBe('echo hello')
 })
 test('empty input sends nothing',()=>expect(encode('').length).toBe(0))
})
