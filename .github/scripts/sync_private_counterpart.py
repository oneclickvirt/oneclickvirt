#!/usr/bin/env python3
"""Apply public changes to the private counterpart without exporting private data."""
import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

PUBLIC = 'oneclickvirt/oneclickvirt'
PRIVATE = 'spiritLHLS/oneclickvirt'
STATE = '.github/public-sync-state.json'


def request(path, method='GET', data=None):
    token = os.environ['SYNC_TOKEN']
    if not token:
        raise RuntimeError('missing credential')
    body = None if data is None else json.dumps(data).encode()
    for attempt in range(3):
        req = urllib.request.Request('https://api.github.com'+path, data=body, method=method,
            headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.github+json',
                     'Content-Type':'application/json','User-Agent':'oneclickvirt-counterpart-sync'})
        try:
            with urllib.request.urlopen(req, timeout=45) as response:
                raw = response.read()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as error:
            # Neither private response bodies nor request payloads reach public logs.
            if error.code not in (429, 500, 502, 503, 504) or attempt == 2:
                raise RuntimeError('counterpart API operation rejected') from None
        except (TimeoutError, urllib.error.URLError):
            if attempt == 2:
                raise RuntimeError('counterpart API transport unavailable') from None
        time.sleep(2 ** attempt)


def tree(repo, sha):
    value = request(f'/repos/{repo}/git/trees/{sha}?recursive=1')
    if value.get('truncated'):
        raise RuntimeError('incomplete tree')
    return {e['path']:e for e in value['tree'] if e['type'] in ('blob','commit')}


def blob(repo, entry):
    if entry is None:
        return b''
    if entry['type'] != 'blob':
        raise RuntimeError('submodule synchronization requires review')
    return base64.b64decode(request(f"/repos/{repo}/git/blobs/{entry['sha']}")['content'])


def merge_content(current, before, after):
    if current == before or current == after:
        return after
    if b'\0' in current + before + after:
        raise RuntimeError('binary conflict')
    # A three-way merge preserves private edits outside the public patch.
    with tempfile.TemporaryDirectory(prefix='private-sync-') as temporary:
        files = [Path(temporary)/name for name in ('private','base','public')]
        for path, content in zip(files, (current, before, after)):
            path.write_bytes(content)
            path.chmod(0o600)
        result = subprocess.run(['git','merge-file','-p',*[str(p) for p in files]],
                                capture_output=True, timeout=30)
        if result.returncode:
            raise RuntimeError('private edit conflicts with public patch')
        return result.stdout


def synchronize():
    if os.environ.get('GITHUB_REPOSITORY') != PUBLIC:
        raise RuntimeError('unexpected source')
    if not request('/repos/'+PRIVATE)['private']:
        raise RuntimeError('counterpart must remain private')
    public_head = request('/repos/'+PUBLIC+'/git/ref/heads/main')['object']['sha']
    after = tree(PUBLIC, public_head)
    for attempt in range(3):
        private_head = request('/repos/'+PRIVATE+'/git/ref/heads/main')['object']['sha']
        private_commit = request(f'/repos/{PRIVATE}/git/commits/{private_head}')
        current = tree(PRIVATE, private_head)
        if STATE not in current:
            raise RuntimeError('private checkpoint has not been initialized')
        state = json.loads(blob(PRIVATE, current[STATE]))
        if state['public_sha'] == public_head:
            print('Private counterpart is synchronized with public commit '+public_head)
            return
        before = tree(PUBLIC, state['public_sha'])
        updates = []
        for path in sorted(before.keys() | after.keys()):
            old, new = before.get(path), after.get(path)
            if old == new:
                continue
            existing = current.get(path)
            if new is None:
                if existing is None:
                    continue
                if existing['sha'] != old['sha']:
                    raise RuntimeError('public deletion conflicts with private edit')
                updates.append({'path':path,'mode':existing['mode'],'type':'blob','sha':None})
                continue
            content = merge_content(blob(PRIVATE, existing), blob(PUBLIC, old), blob(PUBLIC, new))
            value = request(f'/repos/{PRIVATE}/git/blobs','POST',
                            {'content':base64.b64encode(content).decode(),'encoding':'base64'})
            updates.append({'path':path,'mode':new['mode'],'type':'blob','sha':value['sha']})
        updates.append({'path':STATE,'mode':'100644','type':'blob',
                        'content':json.dumps({'public_sha':public_head,'source':PUBLIC},sort_keys=True)+'\n'})
        new_tree = request(f'/repos/{PRIVATE}/git/trees','POST',{'base_tree':private_commit['tree']['sha'],'tree':updates})
        commit = request(f'/repos/{PRIVATE}/git/commits','POST',
                         {'message':'Synchronize public maintenance '+public_head[:12],
                          'tree':new_tree['sha'],'parents':[private_head]})
        try:
            request('/repos/'+PRIVATE+'/git/refs/heads/main','PATCH',{'sha':commit['sha'],'force':False})
        except RuntimeError:
            if attempt == 2:
                raise
            continue
        print('Private counterpart synchronized with public commit '+public_head)
        return
    raise RuntimeError('concurrent counterpart changes exceeded retry budget')


if __name__ == '__main__':
    try:
        synchronize()
    except Exception:
        # No exception message, traceback, private path, source or artifact is exported.
        print('::error::Private synchronization did not complete. Previous private content was preserved.')
        raise SystemExit(1) from None
