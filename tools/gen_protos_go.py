import os
import pathlib
import subprocess
import sys


ROOT = pathlib.Path(__file__).resolve().parents[1]
PROTO_DIR = ROOT / "proto"
OUT_DIR = ROOT / "gen" / "go"


def main() -> int:
    gopath = (
        subprocess.check_output(["go", "env", "GOPATH"], cwd=str(ROOT))
        .decode("utf-8")
        .strip()
    )

    # On Windows these binaries are typically .exe.
    protoc_gen_go = pathlib.Path(gopath) / "bin" / "protoc-gen-go.exe"
    protoc_gen_go_grpc = pathlib.Path(gopath) / "bin" / "protoc-gen-go-grpc.exe"
    if not protoc_gen_go.exists():
        raise FileNotFoundError(f"Missing {protoc_gen_go}. Run: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest")
    if not protoc_gen_go_grpc.exists():
        raise FileNotFoundError(
            f"Missing {protoc_gen_go_grpc}. Run: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"
        )

    protos = [
        PROTO_DIR / "iicpc" / "trading" / "trading.proto",
        PROTO_DIR / "iicpc" / "telemetry" / "telemetry.proto",
    ]
    for p in protos:
        if not p.exists():
            raise FileNotFoundError(str(p))

    OUT_DIR.mkdir(parents=True, exist_ok=True)

    cmd = [
        sys.executable,
        "-m",
        "grpc_tools.protoc",
        f"-I{PROTO_DIR}",
        f"--plugin=protoc-gen-go={protoc_gen_go}",
        f"--plugin=protoc-gen-go-grpc={protoc_gen_go_grpc}",
        f"--go_out={OUT_DIR}",
        "--go_opt=paths=source_relative",
        f"--go-grpc_out={OUT_DIR}",
        "--go-grpc_opt=paths=source_relative",
        *[str(p) for p in protos],
    ]

    print("Generating Go protobuf stubs...")
    print(" ".join(map(str, cmd)))
    env = os.environ.copy()
    # Ensure Go plugin binaries are discoverable (even if GOPATH/bin isn't in PATH).
    env["PATH"] = str(pathlib.Path(gopath) / "bin") + os.pathsep + env.get("PATH", "")
    subprocess.check_call(cmd, cwd=str(ROOT), env=env)
    print("Done.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

