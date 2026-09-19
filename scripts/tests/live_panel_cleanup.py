"""Identity-checked disposal of a successful live panel fixture."""
import json


def remove_owned_panel(docker, container, run_id):
    # Resolve once, then delete the immutable ID: a replacement using the same
    # name after inspect must not become the target of cleanup.
    info = json.loads(docker("inspect", container))[0]
    if info.get("Config", {}).get("Labels", {}).get("ocv.live.run") != run_id:
        raise RuntimeError("panel cleanup ownership changed")
    container_id = info["Id"]
    # The all-in-one image declares anonymous database/storage volumes. -v
    # removes those, but deliberately retains any named volume.
    docker("rm", "-f", "-v", container_id)
