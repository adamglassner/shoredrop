#!/usr/bin/env python3
"""Test SSH/SFTP server for ShoreDrop.

The SSH transport (key exchange, ciphers, MACs, host keys, auth) is the
real libssh 0.10 server implementation, loaded with ctypes, so ShoreDrop's
SSH client is checked against an independent implementation. The SFTP v3
subsystem on top is a small Python implementation serving a directory.

usage: sshd.py PORT ROOTDIR KEYDIR [--kex X] [--cipher Y] [--mac Z] [--hostkey-alg H]
"""
import ctypes, os, sys, struct, stat, signal, argparse

lib = ctypes.CDLL("libssh.so.4")
P = ctypes.c_void_p
for name, res, args in [
    ("ssh_init", ctypes.c_int, []),
    ("ssh_bind_new", P, []),
    ("ssh_bind_options_set", ctypes.c_int, [P, ctypes.c_int, P]),
    ("ssh_bind_listen", ctypes.c_int, [P]),
    ("ssh_bind_accept", ctypes.c_int, [P, P]),
    ("ssh_new", P, []),
    ("ssh_free", None, [P]),
    ("ssh_handle_key_exchange", ctypes.c_int, [P]),
    ("ssh_options_set", ctypes.c_int, [P, ctypes.c_int, P]),
    ("ssh_get_error", ctypes.c_char_p, [P]),
    ("ssh_message_get", P, [P]),
    ("ssh_message_type", ctypes.c_int, [P]),
    ("ssh_message_subtype", ctypes.c_int, [P]),
    ("ssh_message_free", None, [P]),
    ("ssh_message_reply_default", ctypes.c_int, [P]),
    ("ssh_message_auth_user", ctypes.c_char_p, [P]),
    ("ssh_message_auth_password", ctypes.c_char_p, [P]),
    ("ssh_message_auth_pubkey", P, [P]),
    ("ssh_message_auth_publickey_state", ctypes.c_int, [P]),
    ("ssh_message_auth_reply_success", ctypes.c_int, [P, ctypes.c_int]),
    ("ssh_message_auth_reply_pk_ok_simple", ctypes.c_int, [P]),
    ("ssh_message_auth_set_methods", ctypes.c_int, [P, ctypes.c_int]),
    ("ssh_message_channel_request_open_reply_accept", P, [P]),
    ("ssh_message_channel_request_subsystem", ctypes.c_char_p, [P]),
    ("ssh_message_channel_request_reply_success", ctypes.c_int, [P]),
    ("ssh_channel_read", ctypes.c_int, [P, P, ctypes.c_uint32, ctypes.c_int]),
    ("ssh_channel_write", ctypes.c_int, [P, P, ctypes.c_uint32]),
    ("ssh_channel_send_eof", ctypes.c_int, [P]),
    ("ssh_channel_close", ctypes.c_int, [P]),
    ("ssh_disconnect", None, [P]),
    ("ssh_pki_export_pubkey_base64", ctypes.c_int, [P, ctypes.POINTER(ctypes.c_char_p)]),
]:
    f = getattr(lib, name)
    f.restype = res
    f.argtypes = args

USER, PASSWORD = b"tester", b"s3cret pass"

ap = argparse.ArgumentParser()
ap.add_argument("port")
ap.add_argument("root")
ap.add_argument("keydir")
ap.add_argument("--kex")
ap.add_argument("--cipher")
ap.add_argument("--mac")
ap.add_argument("--hostkey", default="ed25519")
ap.add_argument("--hostkey-alg")
ap.add_argument("--rekey", type=int, default=0)
a = ap.parse_args()
ROOT = os.path.abspath(a.root)
allowed_pub = set()
for fn in os.listdir(a.keydir):
    if fn.startswith("client_") and fn.endswith(".pub"):
        parts = open(os.path.join(a.keydir, fn)).read().split()
        allowed_pub.add(parts[1])

signal.signal(signal.SIGCHLD, signal.SIG_IGN)
lib.ssh_init()
bind = lib.ssh_bind_new()
def bopt(code, val):
    if lib.ssh_bind_options_set(bind, code, ctypes.c_char_p(val.encode())) != 0:
        sys.exit("bind option %d failed" % code)
bopt(2, a.port)          # BINDPORT_STR
bopt(0, "127.0.0.1")     # BINDADDR
bopt(3, os.path.join(a.keydir, "host_" + a.hostkey))  # HOSTKEY
if a.kex: bopt(11, a.kex)
if a.cipher: bopt(12, a.cipher); bopt(13, a.cipher)
if a.mac: bopt(14, a.mac); bopt(15, a.mac)
if a.hostkey_alg: bopt(18, a.hostkey_alg)
if lib.ssh_bind_listen(bind) != 0:
    sys.exit("listen failed: %s" % lib.ssh_get_error(bind))
print("listening", flush=True)

# ---------------- SFTP v3 ----------------

def u32(v): return struct.pack(">I", v)
def s(b):
    if isinstance(b, str): b = b.encode()
    return u32(len(b)) + b

class R:
    def __init__(self, b): self.b, self.i = b, 0
    def u32(self):
        v = struct.unpack_from(">I", self.b, self.i)[0]; self.i += 4; return v
    def u64(self):
        v = struct.unpack_from(">Q", self.b, self.i)[0]; self.i += 8; return v
    def s(self):
        n = self.u32(); v = self.b[self.i:self.i+n]; self.i += n; return v
    def attrs(self):
        fl = self.u32(); out = {}
        if fl & 1: out["size"] = self.u64()
        if fl & 2: self.u32(); self.u32()
        if fl & 4: out["perm"] = self.u32()
        if fl & 8: out["atime"] = self.u32(); out["mtime"] = self.u32()
        if fl & 0x80000000:
            for _ in range(self.u32()): self.s(); self.s()
        return out

def local(p):
    p = p.decode()
    if not p.startswith("/"): p = "/" + p
    full = os.path.normpath(ROOT + "/" + p)
    if not (full == ROOT or full.startswith(ROOT + "/")): raise PermissionError
    return full

def virt(full):
    rel = os.path.relpath(full, ROOT)
    return "/" if rel == "." else "/" + rel

def enc_attrs(st):
    return u32(1|2|4|8) + struct.pack(">Q", st.st_size) + u32(st.st_uid) + u32(st.st_gid) + u32(st.st_mode) + u32(int(st.st_atime)) + u32(int(st.st_mtime))

def longname(name, st):
    return "%s 1 u g %d Jan 1 00:00 %s" % (stat.filemode(st.st_mode), st.st_size, name)

def status(rid, code, msg=""):
    return bytes([101]) + u32(rid) + u32(code) + s(msg) + s("")

def err_status(rid, e):
    if isinstance(e, FileNotFoundError): return status(rid, 2, "No such file")
    if isinstance(e, PermissionError): return status(rid, 3, "Permission denied")
    return status(rid, 4, str(e))

def sftp_session(chan):
    handles, nexth = {}, [0]
    buf = b""
    rb = ctypes.create_string_buffer(65536)
    def send(pkt):
        data = u32(len(pkt)) + pkt
        lib.ssh_channel_write(chan, data, len(data))
    def newh(obj):
        nexth[0] += 1; h = b"h%d" % nexth[0]; handles[h] = obj; return h
    while True:
        n = lib.ssh_channel_read(chan, rb, 65536, 0)
        if n <= 0: return
        buf += rb.raw[:n]
        while len(buf) >= 4:
            ln = struct.unpack(">I", buf[:4])[0]
            if len(buf) < 4 + ln: break
            pkt, buf = buf[4:4+ln], buf[4+ln:]
            t = pkt[0]; r = R(pkt[1:])
            if t == 1:
                send(bytes([2]) + u32(3)); continue
            rid = r.u32()
            try:
                if t == 16:  # REALPATH
                    p = r.s() or b"."
                    full = local(p if p != b"." else b"/")
                    send(bytes([104]) + u32(rid) + u32(1) + s(virt(full)) + s(virt(full)) + u32(0))
                elif t in (17, 7):  # STAT/LSTAT
                    f = local(r.s())
                    st = os.stat(f) if t == 17 else os.lstat(f)
                    send(bytes([105]) + u32(rid) + enc_attrs(st))
                elif t == 11:  # OPENDIR
                    f = local(r.s())
                    if not os.path.isdir(f): raise FileNotFoundError
                    send(bytes([102]) + u32(rid) + s(newh(["dir", f, sorted(os.listdir(f)) + [".", ".."]])))
                elif t == 12:  # READDIR
                    hd = handles[r.s()]
                    names = hd[2][:50]; hd[2] = hd[2][50:]
                    if not names:
                        send(status(rid, 1, "EOF"))
                    else:
                        out = bytes([104]) + u32(rid) + u32(len(names))
                        for nm in names:
                            st = os.lstat(os.path.join(hd[1], nm))
                            out += s(nm) + s(longname(nm, st)) + enc_attrs(st)
                        send(out)
                elif t == 3:  # OPEN
                    f = local(r.s()); fl = r.u32(); r.attrs()
                    mode = os.O_RDONLY
                    if fl & 2: mode = os.O_RDWR if fl & 1 else os.O_WRONLY
                    if fl & 8: mode |= os.O_CREAT
                    if fl & 0x10: mode |= os.O_TRUNC
                    if fl & 4: mode |= os.O_APPEND
                    fd = os.open(f, mode, 0o644)
                    send(bytes([102]) + u32(rid) + s(newh(["file", fd])))
                elif t == 5:  # READ
                    hd = handles[r.s()]; off = r.u64(); ln = r.u32()
                    data = os.pread(hd[1], min(ln, 65536), off)
                    if not data: send(status(rid, 1, "EOF"))
                    else: send(bytes([103]) + u32(rid) + s(data))
                elif t == 6:  # WRITE
                    hd = handles[r.s()]; off = r.u64(); data = r.s()
                    os.pwrite(hd[1], data, off); send(status(rid, 0))
                elif t == 4:  # CLOSE
                    hd = handles.pop(r.s())
                    if hd[0] == "file": os.close(hd[1])
                    send(status(rid, 0))
                elif t == 13: os.remove(local(r.s())); send(status(rid, 0))
                elif t == 14: os.mkdir(local(r.s())); send(status(rid, 0))
                elif t == 15: os.rmdir(local(r.s())); send(status(rid, 0))
                elif t == 18:
                    a1, a2 = local(r.s()), local(r.s())
                    if os.path.exists(a2): send(status(rid, 4, "target exists")); continue
                    os.rename(a1, a2); send(status(rid, 0))
                elif t == 9:  # SETSTAT
                    f = local(r.s()); at = r.attrs()
                    if "perm" in at: os.chmod(f, at["perm"] & 0o7777)
                    if "mtime" in at: os.utime(f, (at["atime"], at["mtime"]))
                    send(status(rid, 0))
                else:
                    send(status(rid, 8, "unsupported"))
            except Exception as e:
                send(err_status(rid, e))

def serve(sess):
    if a.rekey:
        v=ctypes.c_uint64(a.rekey)
        print("rekey opt", lib.ssh_options_set(sess, 39, ctypes.byref(v)), flush=True)
    if lib.ssh_handle_key_exchange(sess) != 0:
        print("kex failed:", lib.ssh_get_error(sess), flush=True); return
    authed, chan = False, None
    while True:
        msg = lib.ssh_message_get(sess)
        if not msg: return
        t, st = lib.ssh_message_type(msg), lib.ssh_message_subtype(msg)
        if t == 1:  # auth
            user = lib.ssh_message_auth_user(msg)
            if st == 2 and user == USER and lib.ssh_message_auth_password(msg) == PASSWORD:
                lib.ssh_message_auth_reply_success(msg, 0); authed = True
            elif st == 4 and user == USER:
                key = lib.ssh_message_auth_pubkey(msg)
                b64 = ctypes.c_char_p()
                lib.ssh_pki_export_pubkey_base64(key, ctypes.byref(b64))
                ok = b64.value.decode() in allowed_pub
                state = lib.ssh_message_auth_publickey_state(msg)
                if ok and state == 1:
                    lib.ssh_message_auth_reply_success(msg, 0); authed = True
                elif ok and state == 0:
                    lib.ssh_message_auth_reply_pk_ok_simple(msg)
                else:
                    lib.ssh_message_auth_set_methods(msg, 2 | 4); lib.ssh_message_reply_default(msg)
            else:
                lib.ssh_message_auth_set_methods(msg, 2 | 4); lib.ssh_message_reply_default(msg)
        elif t == 2 and st == 1 and authed:
            chan = lib.ssh_message_channel_request_open_reply_accept(msg)
        elif t == 3 and st == 5 and chan and lib.ssh_message_channel_request_subsystem(msg) == b"sftp":
            lib.ssh_message_channel_request_reply_success(msg)
            lib.ssh_message_free(msg)
            sftp_session(chan)
            lib.ssh_channel_send_eof(chan); lib.ssh_channel_close(chan)
            lib.ssh_disconnect(sess)
            return
        else:
            lib.ssh_message_reply_default(msg)
        lib.ssh_message_free(msg)

while True:
    sess = lib.ssh_new()
    if lib.ssh_bind_accept(bind, sess) != 0:
        continue
    pid = os.fork()
    if pid == 0:
        try: serve(sess)
        finally: os._exit(0)
    lib.ssh_free(sess)
