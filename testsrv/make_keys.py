# Generates throwaway host/client keys and a self-signed certificate for the test servers.
import os, subprocess
from cryptography.hazmat.primitives.asymmetric import ed25519, ec, rsa
from cryptography.hazmat.primitives import serialization as S
os.makedirs("keys", exist_ok=True)
def save(name, k, fmt=S.PrivateFormat.OpenSSH):
    open("keys/"+name, "wb").write(k.private_bytes(S.Encoding.PEM, fmt, S.NoEncryption()))
    open("keys/"+name+".pub", "wb").write(k.public_key().public_bytes(S.Encoding.OpenSSH, S.PublicFormat.OpenSSH) + b" test\n")
save("host_ed25519", ed25519.Ed25519PrivateKey.generate())
save("host_ecdsa256", ec.generate_private_key(ec.SECP256R1()))
save("host_ecdsa384", ec.generate_private_key(ec.SECP384R1()))
save("host_rsa", rsa.generate_private_key(65537, 2048))
save("client_ed25519", ed25519.Ed25519PrivateKey.generate())
save("client_ecdsa", ec.generate_private_key(ec.SECP256R1()))
save("client_rsa_pem", rsa.generate_private_key(65537, 2048), S.PrivateFormat.TraditionalOpenSSL)
save("client_rsa_openssh", rsa.generate_private_key(65537, 3072))
subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ftp.key", "-out", "ftp.crt", "-days", "30", "-subj", "/CN=localhost"], check=True)
