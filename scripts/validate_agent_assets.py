#!/usr/bin/env python3
"""Reject missing, mismatched, or synthetic embedded Agent artifacts."""
import pathlib
import struct
import sys
import tarfile


def validate_assets(asset_dir):
    for arch, machine in (("amd64", 62), ("arm64", 183)):
        name = f"oneclickvirt-agent-linux-{arch}"
        archive = pathlib.Path(asset_dir) / f"{name}.tar.gz"
        with tarfile.open(archive, "r:gz") as tar:
            # CI packages keep the architecture in the member name so an
            # extracted archive is self-describing. Release and Docker
            # workflows historically package the same binary as
            # `oneclickvirt-agent`, which is also the name consumed by the
            # deployment script. Accept both canonical layouts, but never
            # accept a directory or a path outside the archive root.
            member = next(
                (tar.getmember(candidate) for candidate in (name, "oneclickvirt-agent")
                 if candidate in tar.getnames()),
                None,
            )
            if member is None:
                raise ValueError(
                    f"{archive}: missing Agent member (expected {name} or oneclickvirt-agent)"
                )
            if not member.isfile():
                raise ValueError(f"{archive}: Agent must be a regular ELF file")
            with tar.extractfile(member) as binary:
                header = binary.read(64)
        if (len(header) != 64 or header[:6] != b"\x7fELF\x02\x01"
                or struct.unpack_from("<H", header, 18)[0] != machine):
            raise ValueError(f"{archive}: expected Linux {arch} ELF, not a CI stub")


if __name__ == "__main__":
    try:
        validate_assets(sys.argv[1])
    except (IndexError, OSError, KeyError, ValueError, tarfile.TarError) as error:
        sys.exit(f"Agent asset validation failed: {error}")
    print("Real AMD64 and ARM64 Agent assets verified")
