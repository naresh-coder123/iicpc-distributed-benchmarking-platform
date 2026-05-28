import pathlib
import subprocess
import sys


ROOT = pathlib.Path(__file__).resolve().parents[1]
PROTO_DIR = ROOT / "proto"
OUT_DIR = ROOT / "pkg"


def main() -> int:
    protos = [
        PROTO_DIR / "iicpc" / "trading" / "trading.proto",
        PROTO_DIR / "iicpc" / "telemetry" / "telemetry.proto",
    ]
    for p in protos:
        if not p.exists():
            raise FileNotFoundError(str(p))

    cmd = [
        sys.executable,
        "-m",
        "grpc_tools.protoc",
        f"-I{PROTO_DIR}",
        f"--python_out={OUT_DIR}",
        f"--grpc_python_out={OUT_DIR}",
        *[str(p) for p in protos],
    ]

    print("Generating protobuf stubs...")
    print(" ".join(cmd))
    subprocess.check_call(cmd, cwd=str(ROOT))

    print("Done.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

