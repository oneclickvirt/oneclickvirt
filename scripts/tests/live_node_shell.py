"""Command environment for the disposable-node acceptance drivers.

An installer export does not survive into the next SSH exec channel. Include
the standard snap command locations without sourcing user shell startup code.
"""
import shlex

SNAP_PATHS = ("/snap/bin", "/var/lib/snapd/snap/bin")


def node_command(command):
    return ('export PATH="${PATH:-/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin}":'
            + shlex.quote(":".join(SNAP_PATHS)) + "; " + command)
