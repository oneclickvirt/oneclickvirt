#!/usr/bin/env python3
"""Use the official MCP client to exercise actual locally installed artifacts."""
import argparse
import asyncio
import importlib.util
import json
import os
from pathlib import Path
import socketserver
import sys
import threading

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

ROOT=Path(__file__).resolve().parent

def module(path,name):
 spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m

def install(profile,output,live_binary):
 name=profile['repository'].split('/')[1];source=ROOT/'staged'/name
 m=module(source/'scripts/install_agent.py','install_'+name)
 manifest,payload=m.load_payload(lambda p:(source/p).read_bytes())
 binary=None;content=None;record={}
 if live_binary and profile.get('asset_prefix'):
  content,record=m.release_binary(profile,'latest')
  binary=str((output/name/'bin'/(profile['binary']+('.exe' if os.name=='nt' else ''))).resolve())
 m.install(manifest,payload,output/name/'skill',output/name/'mcp',{'commit':'local-generated-candidate','binary':record},binary=binary,binary_content=content)
 return record,binary

async def exercise(profile,output,probe,loopback):
 name=profile['repository'].split('/')[1]
 record,binary=await asyncio.to_thread(install,profile,output,probe)
 env=dict(os.environ)
 env.update(ONECLICKVIRT_MCP_ALLOW_WRITE='0',ONECLICKVIRT_MCP_ALLOW_BENCHMARKS='1')
 if binary:env['ONECLICKVIRT_MCP_BINARY']=binary
 params=StdioServerParameters(command=sys.executable,args=[str(output/name/'mcp/server.py')],env=env)
 async with stdio_client(params) as (reader,writer):
  async with ClientSession(reader,writer) as client:
   await client.initialize()
   tools=(await client.list_tools()).tools
   info=await client.call_tool('integration_info',{})
   assert not info.isError
   result={'repository':profile['repository'],'tools':[t.name for t in tools],'handshake':'passed','binary':record}
   if binary:
    help_result=await client.call_tool('probe_help',{})
    assert not help_result.isError,(name,'help',help_result)
    preset=next(iter(profile['presets']))
    arguments={'preset':preset}
    if name=='pingtest':arguments['target']='127.0.0.1:'+str(loopback)
    if name=='backtrace':arguments['target']='1.1.1.1'
    check=await client.call_tool('run_probe',arguments)
    result['probe_error']=check.isError
    result['probe_result']=[c.text for c in check.content if c.type=='text']
   if name=='hitscounter' and probe:
    check=await client.call_tool('counter_monthly',{'counter':'ecs'})
    result['counter_error']=check.isError
    result['counter_result']=[c.text for c in check.content if c.type=='text']
   if name=='docker' and probe:
    check=await client.call_tool('list_instances',{})
    result['runtime_error']=check.isError
    result['runtime_count']=len(json.loads(check.content[0].text)) if not check.isError else None
   if profile.get('releases') and not profile.get('asset_prefix') and probe:
    check=await client.call_tool('release_assets',{})
    result['release_error']=check.isError
    if check.isError:result['release_result']=[c.text for c in check.content if c.type=='text']
    else:
     value=json.loads(check.content[0].text)
     result['release_version']=value['version'];result['release_asset_count']=len(value['assets'])
     result['release_sample']=value['assets'][:2]
   if profile.get('network') and probe and sys.platform.startswith('linux'):
    check=await client.call_tool('network_status',{})
    result['network_error']=check.isError
    result['network_result']=[c.text for c in check.content if c.type=='text']
 return result

class Handler(socketserver.BaseRequestHandler):
 def handle(self):pass

async def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--probes',action='store_true')
 parser.add_argument('--only',nargs='*')
 parser.add_argument('--output',type=Path,default=ROOT/'native-macos')
 args=parser.parse_args();args.output.mkdir(parents=True,exist_ok=True)
 catalog=json.loads((ROOT/'catalog.json').read_text(encoding="utf-8"))
 selected=[p for p in catalog if p['mcp'] and (not args.only or p['repository'].split('/')[1] in args.only)]
 tcp=socketserver.ThreadingTCPServer(('127.0.0.1',0),Handler);thread=threading.Thread(target=tcp.serve_forever,daemon=True);thread.start()
 results=[]
 try:
  for profile in selected:
   name=profile['repository'].split('/')[1]
   try:
    result=await exercise(profile,args.output,args.probes,tcp.server_address[1])
   except Exception as exc:
    result={'repository':profile['repository'],'failure':str(exc)}
   results.append(result)
   if result.get('failure') or any(result.get(k+'_error') for k in ['probe','counter','release','network']):print(json.dumps(result,ensure_ascii=True)[:3000],flush=True)
   (args.output/'results.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
   print(name,'failed' if result.get('failure') or any(result.get(k+'_error') for k in ['probe','runtime','counter','release','network']) else 'passed',flush=True)
 finally:tcp.shutdown();tcp.server_close()
 bad=[r['repository'] for r in results if r.get('failure') or any(r.get(k+'_error') for k in ['probe','runtime','counter','release','network'])]
 if bad: print('Failures:',bad);raise SystemExit(1)

if __name__=='__main__':asyncio.run(main())
