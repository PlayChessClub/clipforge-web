#!/usr/bin/env python3
# ClipForge web 版一键构建: 交叉编译 windows/linux + zip + deb
#
# 用法(在 web/server 所在仓库内任意位置):
#   python3 build-pkgs.py                          # 默认: 本地交叉编译 windows amd64/arm64
#   python3 build-pkgs.py --only linux-amd64 --deb # 指定 linux 目标并打 deb(CI 内使用)
#
# 说明: 全部纯静态编译(CGO_ENABLED=0)。Linux 版 = CLI(终端) + 浏览器(桌面图标,无 webkit)。
import os, sys, io, time, tarfile, zipfile, subprocess, argparse

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SERVER = os.path.join(ROOT, "web", "server")
REL = os.path.join(ROOT, "web", "release")
VERSION = os.environ.get("CF_VERSION", "3.2.2")

def sh(cmd, cwd=None, env=None):
    e = dict(os.environ, **(env or {}))
    subprocess.run(cmd, shell=True, cwd=cwd, check=True, env=e)

def build_bin(goos, goarch, cgo):
    out = os.path.join(ROOT, f"clipforge-{goos}-{goarch}" + (".exe" if goos == "windows" else ""))
    sh(f"go build -ldflags=\"-s -w\" -o \"{out}\" .", cwd=SERVER,
       env={"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "1" if cgo else "0"})
    return out

def make_zip(bin_path, goos, goarch):
    zp = os.path.join(REL, f"clipforge-{goos}-{goarch}.zip")
    with zipfile.ZipFile(zp, "w", zipfile.ZIP_DEFLATED) as z:
        z.write(bin_path, os.path.basename(bin_path))
    return zp

def ar_member(name, data):
    size = len(data)
    hdr = (b"%-16s%-12s%-6s%-6s%-8s%-10s`\n"
           % (name.encode().ljust(16, b" "), b"0", b"0", b"0", b"100644", str(size).encode()))
    return hdr + data + (b"\n" if size % 2 else b"")

def tar_gz(entries, dirs=()):
    buf = io.BytesIO()
    tf = tarfile.open(fileobj=buf, mode="w:gz", format=tarfile.GNU_FORMAT)
    now = int(time.time())
    for arc, mode in dirs:
        ti = tarfile.TarInfo(arc); ti.type = tarfile.DIRTYPE; ti.mode = mode; ti.mtime = now
        tf.addfile(ti)
    for src, arc, mode in entries:
        ti = tarfile.TarInfo(arc); ti.size = os.path.getsize(src); ti.mode = mode; ti.mtime = now
        with open(src, "rb") as f:
            tf.addfile(ti, f)
    tf.close()
    return buf.getvalue()

def make_deb(bin_path, goarch):
    # 图标:优先仓库内置 web/assets(CI 可用),回退本地 build 产物
    icon = os.path.join(ROOT, "web", "assets", "icon-512.png")
    if not os.path.exists(icon):
        icon = os.path.join(ROOT, "build", "AppIcon.iconset", "icon_512x512@2x.png")
    lic = os.path.join(ROOT, "LICENSE")
    desktop = ("[Desktop Entry]\nType=Application\nName=ClipForge AI\n"
               "Comment=AI 视频/图片/语音生成客户端(本地 API 代理)\n"
               "Exec=clipforge\nIcon=clipforge\nTerminal=false\n"
               "Categories=AudioVideo;AudioVideoEditing;\nStartupNotify=false\n")
    control = (f"Package: clipforge\nVersion: {VERSION}\nSection: utils\nPriority: optional\n"
               f"Architecture: {goarch}\nInstalled-Size: {os.path.getsize(bin_path) // 1024 + 900}\n"
               f"Maintainer: PlayChessClub <playchessclub@users.noreply.github.com>\n"
               f"Homepage: https://github.com/PlayChessClub/videogenerator\n"
               f"Depends: xdg-utils\n"
               f"Description: ClipForge AI - 本地 API 代理客户端(视频/图片/语音生成)\n"
               f" 单文件静态 Go 程序, 前端已内嵌。终端运行进命令行菜单(clipforge --help);\n"
               f" 桌面图标启动自动开浏览器访问 127.0.0.1:8731。\n"
               f" 作为 DashScope(阿里云百炼) API 代理, API Key 保存在本机, 不回显不上报。\n")
    tmp_desktop = os.path.join("/tmp", f"clipforge-desktop-{goarch}")
    tmp_control = os.path.join("/tmp", f"clipforge-control-{goarch}")
    open(tmp_desktop, "w").write(desktop)
    open(tmp_control, "w").write(control)
    D = [("./usr", 0o755), ("./usr/bin", 0o755), ("./usr/share", 0o755),
         ("./usr/share/applications", 0o755), ("./usr/share/icons", 0o755),
         ("./usr/share/icons/hicolor", 0o755), ("./usr/share/icons/hicolor/512x512", 0o755),
         ("./usr/share/icons/hicolor/512x512/apps", 0o755),
         ("./usr/share/doc", 0o755), ("./usr/share/doc/clipforge", 0o755)]
    deb = (b"!<arch>\n"
           + ar_member("debian-binary", b"2.0\n")
           + ar_member("control.tar.gz", tar_gz([(tmp_control, "./control", 0o644)]))
           + ar_member("data.tar.gz", tar_gz(
               [(bin_path, "./usr/bin/clipforge", 0o755),
                (tmp_desktop, "./usr/share/applications/clipforge.desktop", 0o644),
                (icon, "./usr/share/icons/hicolor/512x512/apps/clipforge.png", 0o644),
                (lic, "./usr/share/doc/clipforge/copyright", 0o644)], dirs=D)))
    out = os.path.join(REL, f"clipforge_{VERSION}_{goarch}.deb")
    open(out, "wb").write(deb)
    os.remove(tmp_desktop); os.remove(tmp_control)
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", default="windows-amd64,windows-arm64",
                    help="逗号分隔 goos-goarch 列表(默认 windows 双架构)")
    ap.add_argument("--deb", action="store_true", help="为 linux 目标打 deb 安装包")
    ap.add_argument("--cgo", action="store_true", help="(兼容参数,已弃用:现在一律静态编译)")
    args = ap.parse_args()

    os.makedirs(REL, exist_ok=True)
    for t in args.only.split(","):
        goos, goarch = t.split("-")
        b = build_bin(goos, goarch, False)  # 一律 CGO_ENABLED=0 静态
        z = make_zip(b, goos, goarch)
        line = f"{goos}/{goarch}: {os.path.getsize(b)} bytes -> {os.path.basename(z)}"
        if args.deb and goos == "linux":
            d = make_deb(b, goarch)
            line += f" + {os.path.basename(d)}"
        print(line)

if __name__ == "__main__":
    main()
