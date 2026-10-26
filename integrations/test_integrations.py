#!/usr/bin/env python3
"""Behavioral tests for generated MCP/Skill distributions; no live host mutations."""
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import urllib.request
from unittest.mock import patch

ROOT=Path(__file__).resolve().parent

def module(path,name):
 spec=importlib.util.spec_from_file_location(name,path)
 m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
server=module(ROOT/'templates/server.py','agent_server')
generator=module(ROOT/'generate.py','agent_generator')
CATALOG=json.loads((ROOT/'catalog.json').read_text(encoding="utf-8"))

class IntegrationTests(unittest.TestCase):
 def setUp(self):
  self.temp=tempfile.TemporaryDirectory();self.root=Path(self.temp.name)
  self.env=patch.dict(os.environ,{'ONECLICKVIRT_MCP_ALLOW_WRITE':'0','ONECLICKVIRT_MCP_ALLOW_BENCHMARKS':'0'},clear=False);self.env.start()
 def tearDown(self):self.env.stop();self.temp.cleanup()
 def profile(self,name):return copy.deepcopy(next(p for p in CATALOG if p['repository']=='oneclickvirt/'+name))
 def adapter(self,name):
  p=self.root/(name+'.json');p.write_text(json.dumps(self.profile(name)));return server.Adapter(p)
 def installer(self,name='basics'):
  p=self.profile(name);target=generator.generate(p,self.root/name)
  return module(target/'scripts/install_agent.py','installer_'+name),target
 def test_all_profiles_generate_verified_installable_skills(self):
  for profile in CATALOG:
   name=profile['repository'].split('/')[1]
   with self.subTest(repository=name):
    m,source=self.installer(name)
    manifest,payload=m.load_payload(lambda path:(source/path).read_bytes())
    for entry in ['SKILL.md','SKILL_ZH.md']:
     self.assertEqual((source/entry).read_bytes(),(source/'skills'/profile['skill_name']/entry).read_bytes())
     self.assertNotIn(entry,manifest['files'])
    skill=self.root/'skills'/name;mcp=self.root/'mcp'/name
    result=m.install(manifest,payload,skill,mcp,{'commit':'a'*40})
    self.assertTrue((skill/'SKILL.md').is_file())
    self.assertEqual(bool(profile['mcp']),result['mcp_installed'])
    self.assertEqual(bool(profile['mcp']),(mcp/'server.py').exists())
    self.assertFalse((skill/profile['skill_name']).exists())
    (skill/'personal.txt').write_text('retained')
    m.install(manifest,payload,skill,mcp,{'commit':'b'*40})
    self.assertEqual((skill/'personal.txt').read_text(encoding="utf-8"),'retained')
    self.assertEqual(json.loads((skill/'install.json').read_text(encoding="utf-8"))['commit'],'b'*40)
 def test_tampered_payload_never_replaces_installed_files(self):
  m,source=self.installer();target=self.root/'installed';target.mkdir();(target/'SKILL.md').write_text('original')
  original=(source/'agent/server.py').read_bytes()
  with self.assertRaises(ValueError):
   m.load_payload(lambda p:original+b'corrupt' if p=='agent/server.py' else (source/p).read_bytes())
  self.assertEqual((target/'SKILL.md').read_text(encoding="utf-8"),'original')
 def test_manifest_cannot_add_arbitrary_destination(self):
  m,source=self.installer();raw=json.loads((source/'agent/manifest.json').read_text(encoding="utf-8"));raw['files']['../outside']='a'*64
  with self.assertRaises(ValueError):m.load_payload(lambda p:json.dumps(raw).encode() if p=='agent/manifest.json' else (source/p).read_bytes())
 def test_skill_only_does_not_install_mcp(self):
  m,source=self.installer();manifest,payload=m.load_payload(lambda p:(source/p).read_bytes())
  result=m.install(manifest,payload,self.root/'skill',self.root/'mcp',{'commit':'a'*40},True)
  self.assertFalse(result['mcp_installed']);self.assertFalse((self.root/'mcp').exists())
 def test_client_examples_keep_each_clients_native_config_shape(self):
  m,source=self.installer();manifest,payload=m.load_payload(lambda p:(source/p).read_bytes())
  skill=self.root/'skill';mcp=self.root/'mcp'
  m.install(manifest,payload,skill,mcp,{'commit':'a'*40},binary='/absolute/probe')
  name='oneclickvirt-basics'
  self.assertEqual(json.loads((skill/'mcp.claude.json').read_text())["mcpServers"][name]['args'],[str(mcp/'server.py')])
  self.assertEqual(json.loads((skill/'mcp.openclaw.json').read_text())["mcp"]["servers"][name]['env']['ONECLICKVIRT_MCP_BINARY'],str(Path('/absolute/probe').resolve()))
  try:import tomllib
  except ImportError:return
  value=tomllib.loads((skill/'mcp.codex.toml').read_text(encoding='utf-8'))['mcp_servers'][name]
  self.assertEqual(value['command'],sys.executable);self.assertEqual(value['tool_timeout_sec'],180)
 def test_symlink_destinations_are_preserved(self):
  if os.name=='nt':self.skipTest('Unix symlink test')
  m,source=self.installer();manifest,payload=m.load_payload(lambda p:(source/p).read_bytes())
  actual=self.root/'actual';actual.mkdir();link=self.root/'link';link.symlink_to(actual,target_is_directory=True)
  with self.assertRaises(ValueError):m.install(manifest,payload,link,self.root/'mcp',{'commit':'a'*40})
  self.assertFalse(list(actual.iterdir()))
 def test_source_resolves_once_then_downloads_by_commit(self):
  m,source=self.installer();calls=[]
  def fetch(url):
   calls.append(url)
   return json.dumps({'sha':'d'*40}).encode() if '/commits/' in url else b'pinned'
  with patch.object(m,'fetch',side_effect=fetch):
   commit,reader=m.resolve_source('github','main');self.assertEqual(reader('agent/server.py'),b'pinned')
  self.assertEqual(commit,'d'*40);self.assertEqual(len([x for x in calls if '/commits/' in x]),1)
  self.assertIn('/'+'d'*40+'/agent/server.py',calls[-1])
 def test_cnb_downloads_by_its_own_snapshot(self):
  m,_=self.installer()
  with patch.object(m.subprocess,'check_output',return_value='e'*40+'\trefs/heads/main\n'),patch.object(m,'fetch',return_value=b'fixed') as fetch:
   commit,reader=m.resolve_source('cnb','main');reader('agent/server.py')
   self.assertEqual(commit,'e'*40);self.assertIn('/-/git/raw/'+'e'*40+'/',fetch.call_args.args[0])
 def test_mutable_release_tag_uses_current_digest_not_a_version_cache(self):
  m,_=self.installer();p=self.profile('basics');content=b'real executable';digest=hashlib.sha256(content).hexdigest()
  asset={'name':'basics-linux-amd64','digest':'sha256:'+digest,'browser_download_url':'https://github.com/oneclickvirt/basics/releases/download/output/basics-linux-amd64'}
  release={'tag_name':'output','draft':False,'prerelease':False,'assets':[asset]}
  with patch.object(m.platform,'system',return_value='Linux'),patch.object(m.platform,'machine',return_value='x86_64'),patch.object(m,'fetch',side_effect=[json.dumps(release).encode(),content]):
   data,record=m.release_binary(p,'latest');self.assertEqual(data,content);self.assertEqual(record['archive_sha256'],digest)
  with patch.object(m.platform,'system',return_value='Linux'),patch.object(m.platform,'machine',return_value='aarch64'),patch.object(m,'fetch',return_value=json.dumps(release).encode()):
   with self.assertRaises(ValueError):m.release_binary(p,'latest')
 def test_binary_checksum_failure_is_not_ignored(self):
  m,_=self.installer();p=self.profile('basics')
  release={'tag_name':'x','draft':False,'prerelease':False,'assets':[{'name':'basics-linux-amd64','digest':'sha256:'+'0'*64,'browser_download_url':'https://github.com/oneclickvirt/basics/releases/download/x/basics-linux-amd64'}]}
  with patch.object(m.platform,'system',return_value='Linux'),patch.object(m.platform,'machine',return_value='x86_64'),patch.object(m,'fetch',side_effect=[json.dumps(release).encode(),b'corrupt']):
   with self.assertRaises(ValueError):m.release_binary(p,'latest')
 def test_redirect_never_forwards_github_token_to_another_host(self):
  m,_=self.installer()
  request=urllib.request.Request('https://api.github.com/example',headers={'Authorization':'Bearer private'})
  target=m.SafeRedirect().redirect_request(request,None,302,'found',{},'https://external.example/target')
  self.assertIsNone(target.get_header('Authorization'))
 def test_string_schema_limits_are_enforced_before_network(self):
  with patch.object(server,'github_json') as fetch:
   with self.assertRaises(ValueError):self.adapter('kvm_images').call('release_assets',{'filter':'x'*129})
   fetch.assert_not_called()
 def test_podman_names_array_resolves_without_exposing_other_fields(self):
  adapter=self.adapter('podman')
  with patch.object(server,'execute',return_value={'ID':'a','Names':['sandbox'],'State':'exited','Labels':{'secret':'hidden'}}):
   value=adapter.call('instance_status',{'id':'sandbox'})
   self.assertEqual(value['Names'],'sandbox');self.assertNotIn('secret',json.dumps(value))
 def test_writes_are_not_advertised_or_callable_by_default(self):
  for name in ['docker','podman','containerd','incus','lxd','pve','qemu','kubevirt']:
   adapter=self.adapter(name);self.assertNotIn('instance_action',[t['name'] for t in adapter.tools()])
   with self.assertRaises(ValueError):adapter.call('instance_action',{'id':'vm1','action':'stop'})
 def test_invalid_names_and_unknown_parameters_do_not_run_commands(self):
  adapter=self.adapter('docker')
  with patch.object(server,'execute') as run:
   for args in [{'id':'--all'},{'id':'vm; rm -rf /'},{'id':'vm1','extra':True},{'id':1},{'id':None}]:
    with self.assertRaises(ValueError):adapter.call('instance_status',args)
   run.assert_not_called()
 def test_docker_list_redacts_private_fields(self):
  adapter=self.adapter('docker');data={'ID':'abc','Names':'vm1','Image':'alpine','State':'running','Config':{'Env':['SECRET=private']}}
  with patch.object(server,'execute',return_value=data):value=adapter.call('list_instances',{})
  self.assertNotIn('SECRET',json.dumps(value));self.assertEqual(value[0]['Names'],'vm1')
 def test_pve_write_uses_node_and_guest_type_from_cluster(self):
  adapter=self.adapter('pve')
  with patch.dict(os.environ,{'ONECLICKVIRT_MCP_ALLOW_WRITE':'1'}),patch.object(server,'execute',side_effect=[[{'vmid':123,'node':'node-a','type':'lxc'}],'task']) as run:
   adapter.call('instance_action',{'id':'123','action':'stop'})
  self.assertEqual(run.call_args.args[0],['pvesh','create','/nodes/node-a/lxc/123/status/shutdown'])
 def test_all_native_actions_use_argv_and_never_delete(self):
  for name,binary in [('docker','docker'),('containerd','nerdctl'),('podman','podman'),('incus','incus'),('lxd','lxc')]:
   with self.subTest(runtime=name):
    adapter=self.adapter(name)
    listing=[{'name':'vm1','status':'Stopped'}] if name in ['incus','lxd'] else {'Names':'vm1','ID':'123'}
    with patch.dict(os.environ,{'ONECLICKVIRT_MCP_ALLOW_WRITE':'1'}),patch.object(server,'execute',side_effect=[listing,'ok']) as run:
     adapter.call('instance_action',{'id':'vm1','action':'start'});self.assertEqual(run.call_args.args[0],[binary,'start','vm1'])
 def test_qemu_preserves_json_like_instance_name(self):
  adapter=self.adapter('qemu')
  with patch.object(server,'execute',return_value='null') as run:
   self.assertEqual(adapter.instances(),[{'name':'null'}]);self.assertFalse(run.call_args.kwargs['parse_json'])
 def test_kubevirt_namespace_and_virtctl_are_explicit(self):
  adapter=self.adapter('kubevirt');data={'items':[{'metadata':{'name':'vm1'},'status':{'ready':False}}]}
  with patch.dict(os.environ,{'ONECLICKVIRT_KUBE_NAMESPACE':'sandbox','ONECLICKVIRT_MCP_ALLOW_WRITE':'1'}),patch.object(server,'execute',side_effect=[data,'ok']) as run:
   adapter.call('instance_action',{'id':'vm1','action':'start'});self.assertEqual(run.call_args.args[0],['virtctl','start','vm1','--namespace','sandbox'])
 def test_probe_target_and_resource_gates(self):
  with patch.object(server,'execute') as run:
   with self.assertRaises(ValueError):self.adapter('cputest').call('run_probe',{'preset':'short'})
   with self.assertRaises(ValueError):self.adapter('pingtest').call('run_probe',{'preset':'tcp'})
   with self.assertRaises(ValueError):self.adapter('backtrace').call('run_probe',{'preset':'ip-info','target':'example.com'})
   run.assert_not_called()
 def test_bounded_probe_uses_dedicated_scratch_directory(self):
  adapter=self.adapter('disktest')
  with patch.dict(os.environ,{'ONECLICKVIRT_MCP_ALLOW_BENCHMARKS':'1'}),patch.object(server,'execute',return_value={'ok':True}) as run:
   adapter.call('run_probe',{'preset':'short'})
   args=run.call_args.args[0];scratch=run.call_args.kwargs['cwd']
   self.assertEqual((Path(scratch)/args[args.index('-p')+1]).resolve(),Path(scratch).resolve())
   self.assertNotIn('{scratch}',args);self.assertFalse(Path(scratch).exists())
 def test_release_pins_exact_tag_and_preserves_asset_digest(self):
  adapter=self.adapter('pve_kvm_images');data={'tag_name':'openeuler-22.03','published_at':'today','html_url':'release','assets':[{'name':'image-arm64.qcow2','digest':'sha256:abc','size':123,'browser_download_url':'asset'}]}
  with patch.object(server,'github_json',return_value=data) as fetch:
   value=adapter.call('release_assets',{'version':'openeuler-22.03','filter':'arm64'})
   self.assertIn('/tags/openeuler-22.03',fetch.call_args.args[0]);self.assertEqual(value['assets'][0]['digest'],'sha256:abc')
 def test_protocol_notifications_errors_and_recovery(self):
  adapter=self.adapter('basics');out=io.StringIO()
  messages=['not JSON',json.dumps({'jsonrpc':'2.0','method':'notifications/initialized'}),json.dumps({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2024-11-05'}}),json.dumps({'jsonrpc':'2.0','id':2,'method':'unknown'}),json.dumps({'jsonrpc':'2.0','id':3,'method':'tools/call','params':{'name':'run_probe','arguments':{'preset':'invalid'}}}),json.dumps({'jsonrpc':'2.0','id':4,'method':'ping'})]
  server.serve(adapter,io.StringIO('\n'.join(messages)),out);replies=[json.loads(x) for x in out.getvalue().splitlines()]
  self.assertEqual(len(replies),5);self.assertEqual(replies[0]['error']['code'],-32700)
  self.assertEqual(replies[1]['result']['protocolVersion'],'2024-11-05');self.assertTrue(replies[3]['result']['isError']);self.assertEqual(replies[4]['result'],{})
 def test_protocol_transports_unicode_with_a_legacy_console_encoding(self):
  buffer=io.BytesIO();out=io.TextIOWrapper(buffer,encoding='cp1252')
  message=json.dumps({'jsonrpc':'2.0','id':1,'method':'tools/call','params':{'name':'integration_info','arguments':{}}})
  server.serve(self.adapter('pve'),io.StringIO(message),out);out.flush()
  reply=json.loads(buffer.getvalue().decode('ascii'))
  self.assertFalse(reply['result']['isError'])
  self.assertIn('purpose_zh',json.loads(reply['result']['content'][0]['text']))
 def test_real_subprocess_timeout_and_stdout_json(self):
  self.assertEqual(server.execute([sys.executable,'-c','print(\'{"real":true}\')']),{'real':True})
  with self.assertRaises(ValueError):server.execute([sys.executable,'-c','import time; time.sleep(5)'],timeout=0.1)

if __name__=='__main__':unittest.main(verbosity=2)
