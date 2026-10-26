#!/usr/bin/env python3
"""Exercise a caller-provisioned disposable runtime instance with the official MCP SDK."""
import argparse
import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

ROOT = Path(__file__).resolve().parent


async def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runtime', required=True, choices=['docker', 'podman', 'containerd', 'incus', 'lxd', 'qemu', 'pve', 'kubevirt'])
    parser.add_argument('--id', required=True, help='Caller-owned disposable instance; this script never creates or deletes instances')
    parser.add_argument('--binary', help='Absolute path to a native CLI or container-exec wrapper')
    parser.add_argument('--readonly', action='store_true')
    args = parser.parse_args()
    profile = ROOT / 'staged' / args.runtime / 'agent' / 'profile.json'
    env = dict(os.environ, ONECLICKVIRT_MCP_ALLOW_WRITE='0' if args.readonly else '1')
    if args.binary:
        env['ONECLICKVIRT_MCP_BINARY'] = args.binary
    params = StdioServerParameters(command=sys.executable, args=[str(ROOT/'templates/server.py'), '--profile', str(profile)], env=env)
    evidence = []
    async with stdio_client(params) as (reader, writer):
        async with ClientSession(reader, writer) as client:
            await client.initialize()
            async def call(name, arguments):
                reply = await client.call_tool(name, arguments)
                if reply.isError:
                    raise RuntimeError(str(reply.content))
                value = json.loads(reply.content[0].text)
                evidence.append({'tool': name, 'arguments': arguments, 'result': value})
                return value
            await call('list_instances', {})
            await call('instance_status', {'id': args.id})
            if not args.readonly:
                for action in ['start', 'restart', 'stop']:
                    await call('instance_action', {'id': args.id, 'action': action})
                    # Runtime commands may complete before the state transition.
                    wanted = 'stop' if action == 'stop' else 'run'
                    for attempt in range(45):
                        status = await call('instance_status', {'id': args.id})
                        state = str(status.get('state') or status.get('State') or status.get('status') or status.get('Status') or '').lower()
                        if (wanted == 'run' and ('running' in state or state.startswith('up'))) or (wanted == 'stop' and any(s in state for s in ['stopped', 'exited', 'shut off', 'created'])):
                            break
                        await asyncio.sleep(1)
                    else:
                        raise RuntimeError('Instance did not reach the expected state: ' + str(status))
    print(json.dumps({'runtime': args.runtime, 'instance': args.id, 'readonly': args.readonly, 'checks': evidence}, indent=2))


if __name__ == '__main__':
    asyncio.run(main())
