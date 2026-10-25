#!/usr/bin/env python3
"""Minimal threaded FTP/FTPS test server for ShoreDrop's integration tests.

usage: ftpd.py PORT IMPLICIT_PORT ROOT CERT KEY [--no-mlsd] [--no-epsv] [--dos]
"""
import socket, socketserver, ssl, os, sys, threading, time, stat, argparse

ap = argparse.ArgumentParser()
ap.add_argument("port", type=int)
ap.add_argument("iport", type=int)
ap.add_argument("root")
ap.add_argument("cert")
ap.add_argument("key")
ap.add_argument("--no-mlsd", action="store_true")
ap.add_argument("--no-epsv", action="store_true")
ap.add_argument("--dos", action="store_true")
ap.add_argument("--pasv-ip", default=None)
a = ap.parse_args()
ROOT = os.path.abspath(a.root)
USER, PASS = "tester", "s3cret pass"
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(a.cert, a.key)

class H(socketserver.StreamRequestHandler):
    def setup(self):
        self.implicit = self.server.server_address[1] == a.iport
        if self.implicit:
            self.request = ctx.wrap_socket(self.request, server_side=True)
        super().setup()
        self.cwd = "/"; self.user = None; self.authed = False
        self.prot = False; self.tls = self.implicit
        self.pasv = None; self.port = None; self.rest = 0; self.rnfr = None

    def reply(self, line):
        self.wfile.write((line + "\r\n").encode()); self.wfile.flush()

    def path(self, arg):
        p = arg if arg.startswith("/") else (self.cwd.rstrip("/") + "/" + arg)
        full = os.path.normpath(ROOT + "/" + p)
        if not (full == ROOT or full.startswith(ROOT + "/")): raise PermissionError("outside root")
        return full

    def virt(self, full):
        rel = os.path.relpath(full, ROOT)
        return "/" if rel == "." else "/" + rel

    def data(self):
        if self.pasv:
            s, _ = self.pasv.accept(); self.pasv.close(); self.pasv = None
        elif self.port:
            s = socket.create_connection(self.port); self.port = None
        else:
            raise RuntimeError("no data connection")
        if self.prot:
            s = ctx.wrap_socket(s, server_side=True)
        return s

    def close_data(self, s):
        if isinstance(s, ssl.SSLSocket):
            try: s = s.unwrap()
            except Exception: pass
        s.close()

    def handle(self):
        self.reply("220 ShoreDrop test FTP server ready")
        while True:
            line = self.rfile.readline()
            if not line: return
            line = line.decode("utf-8", "replace").rstrip("\r\n")
            cmd, _, arg = line.partition(" ")
            cmd = cmd.upper()
            try:
                if not self.dispatch(cmd, arg): return
            except FileNotFoundError:
                self.reply("550 No such file or directory")
            except PermissionError as e:
                self.reply("550 Permission denied")
            except Exception as e:
                self.reply("451 Error: %s" % e)

    def dispatch(self, cmd, arg):
        if cmd == "AUTH" and arg.upper() in ("TLS", "SSL"):
            self.reply("234 AUTH TLS ok")
            self.request = ctx.wrap_socket(self.request, server_side=True)
            self.rfile = self.request.makefile("rb"); self.wfile = self.request.makefile("wb")
            self.tls = True; return True
        if cmd == "USER": self.user = arg; self.reply("331 Password required"); return True
        if cmd == "PASS":
            if self.user == USER and arg == PASS:
                self.authed = True; self.reply("230 Logged in")
            else: self.reply("530 Login incorrect")
            return True
        if cmd == "QUIT": self.reply("221 Bye"); return False
        if cmd == "FEAT":
            feats = ["UTF8", "SIZE", "MDTM", "REST STREAM", "PBSZ", "PROT", "AUTH TLS"]
            if not a.no_mlsd: feats.append("MLSD")
            if not a.no_epsv: feats.append("EPSV")
            self.wfile.write(("211-Features:\r\n" + "".join(" %s\r\n" % f for f in feats) + "211 End\r\n").encode()); self.wfile.flush()
            return True
        if not self.authed:
            self.reply("530 Please login"); return True
        if cmd == "PBSZ": self.reply("200 PBSZ=0")
        elif cmd == "PROT": self.prot = arg.upper() == "P"; self.reply("200 Protection set")
        elif cmd == "OPTS": self.reply("200 OK")
        elif cmd == "TYPE": self.reply("200 Type set")
        elif cmd == "SYST": self.reply("215 UNIX Type: L8")
        elif cmd == "NOOP": self.reply("200 NOOP ok")
        elif cmd == "PWD": self.reply('257 "%s" is current directory' % self.cwd)
        elif cmd == "CWD":
            f = self.path(arg)
            if not os.path.isdir(f): raise FileNotFoundError
            self.cwd = self.virt(f); self.reply("250 OK")
        elif cmd == "CDUP": self.cwd = self.virt(self.path("..")); self.reply("250 OK")
        elif cmd == "PASV":
            ls = socket.socket(); ls.bind(("127.0.0.1", 0)); ls.listen(1); self.pasv = ls
            p = ls.getsockname()[1]
            ip = (a.pasv_ip or "127.0.0.1").replace(".", ",")
            self.reply("227 Entering Passive Mode (%s,%d,%d)" % (ip, p >> 8, p & 255))
        elif cmd == "EPSV":
            if a.no_epsv: self.reply("500 Unknown command"); return True
            ls = socket.socket(); ls.bind(("127.0.0.1", 0)); ls.listen(1); self.pasv = ls
            self.reply("229 Entering Extended Passive Mode (|||%d|)" % ls.getsockname()[1])
        elif cmd == "PORT":
            n = [int(x) for x in arg.split(",")]
            self.port = (".".join(map(str, n[:4])), n[4] * 256 + n[5]); self.reply("200 PORT ok")
        elif cmd == "EPRT":
            parts = arg.split(arg[0]); self.port = (parts[2], int(parts[3])); self.reply("200 EPRT ok")
        elif cmd == "REST": self.rest = int(arg); self.reply("350 Restarting at %d" % self.rest)
        elif cmd in ("LIST", "MLSD", "NLST"):
            target = arg
            if target.startswith("-"): target = ""
            if cmd == "MLSD" and a.no_mlsd: self.reply("500 Unknown command"); return True
            f = self.path(target or ".")
            names = sorted(os.listdir(f))
            lines = []
            for n in names:
                st = os.lstat(os.path.join(f, n))
                if cmd == "MLSD":
                    t = "dir" if stat.S_ISDIR(st.st_mode) else "file"
                    lines.append("type=%s;size=%d;modify=%s;UNIX.mode=%04o;perm=rw; %s" % (t, st.st_size, time.strftime("%Y%m%d%H%M%S", time.gmtime(st.st_mtime)), st.st_mode & 0o7777, n))
                elif cmd == "NLST":
                    lines.append(n)
                elif a.dos:
                    d = time.localtime(st.st_mtime)
                    sz = "<DIR>" if stat.S_ISDIR(st.st_mode) else str(st.st_size)
                    lines.append("%s       %s %s" % (time.strftime("%m-%d-%y  %I:%M%p", d), sz.rjust(14) if sz != "<DIR>" else "<DIR>         ", n))
                else:
                    d = time.localtime(st.st_mtime)
                    lines.append("%s   1 owner group %10d %s %s" % (stat.filemode(st.st_mode), st.st_size, time.strftime("%b %d %H:%M", d), n))
            self.reply("150 Opening data connection")
            s = self.data()
            s.sendall(("".join(l + "\r\n" for l in lines)).encode())
            self.close_data(s); self.reply("226 Transfer complete")
        elif cmd == "RETR":
            f = self.path(arg)
            if not os.path.isfile(f): raise FileNotFoundError
            self.reply("150 Opening data connection")
            s = self.data()
            with open(f, "rb") as fh:
                fh.seek(self.rest); self.rest = 0
                while True:
                    b = fh.read(65536)
                    if not b: break
                    s.sendall(b)
            self.close_data(s); self.reply("226 Transfer complete")
        elif cmd in ("STOR", "APPE"):
            f = self.path(arg)
            self.reply("150 Ok to send data")
            s = self.data()
            mode = "ab" if cmd == "APPE" else ("r+b" if self.rest and os.path.exists(f) else "wb")
            with open(f, mode) as fh:
                if cmd == "STOR" and self.rest: fh.seek(self.rest); fh.truncate()
                self.rest = 0
                while True:
                    try: b = s.recv(65536)
                    except ssl.SSLEOFError: b = b""
                    if not b: break
                    fh.write(b)
            self.close_data(s); self.reply("226 Transfer complete")
        elif cmd == "SIZE":
            f = self.path(arg)
            if not os.path.isfile(f): raise FileNotFoundError
            self.reply("213 %d" % os.path.getsize(f))
        elif cmd == "MDTM":
            self.reply("213 " + time.strftime("%Y%m%d%H%M%S", time.gmtime(os.path.getmtime(self.path(arg)))))
        elif cmd == "DELE":
            f = self.path(arg)
            if not os.path.isfile(f): raise FileNotFoundError
            os.remove(f); self.reply("250 Deleted")
        elif cmd == "MKD": f = self.path(arg); os.mkdir(f); self.reply('257 "%s" created' % self.virt(f))
        elif cmd == "RMD": os.rmdir(self.path(arg)); self.reply("250 Removed")
        elif cmd == "RNFR":
            f = self.path(arg)
            if not os.path.exists(f): raise FileNotFoundError
            self.rnfr = f; self.reply("350 Ready for RNTO")
        elif cmd == "RNTO": os.rename(self.rnfr, self.path(arg)); self.rnfr = None; self.reply("250 Renamed")
        elif cmd == "SITE":
            sub, _, rest = arg.partition(" ")
            if sub.upper() == "CHMOD":
                mode, _, p = rest.partition(" ")
                os.chmod(self.path(p), int(mode, 8)); self.reply("200 CHMOD ok")
            else: self.reply("500 Unknown SITE command")
        else:
            self.reply("502 Command not implemented")
        return True

class S(socketserver.ThreadingTCPServer):
    allow_reuse_address = True; daemon_threads = True

for p in (a.port, a.iport):
    srv = S(("127.0.0.1", p), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
print("listening", flush=True)
while True: time.sleep(3600)
