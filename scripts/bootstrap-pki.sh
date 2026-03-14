#!/bin/bash
set -euo pipefail
D="${1:-./certs}"; mkdir -p "$D"

echo "══════════════════════════════════════════"
echo " EdgeFlux PKI Bootstrap"
echo "══════════════════════════════════════════"

echo -e "\n[1/4] Root CA (EC P-384)..."
openssl ecparam -genkey -name secp384r1 -noout -out "$D/root-ca-key.pem" 2>/dev/null
openssl req -new -x509 -sha384 -key "$D/root-ca-key.pem" -out "$D/root-ca.pem" -days 3650 \
  -subj "/C=GB/O=EdgeFlux Systems/CN=EdgeFlux Root CA" 2>/dev/null
echo "  ✓ root-ca.pem"

echo -e "\n[2/4] Intermediate Enrollment CA..."
openssl ecparam -genkey -name secp384r1 -noout -out "$D/intermediate-ca-key.pem" 2>/dev/null
openssl req -new -sha384 -key "$D/intermediate-ca-key.pem" -out "$D/int.csr" \
  -subj "/C=GB/O=EdgeFlux Systems/OU=Device Provisioning/CN=EdgeFlux Enrollment CA" 2>/dev/null
cat > "$D/_ext.cnf" <<EOF
basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,digitalSignature,keyCertSign
extendedKeyUsage=serverAuth,clientAuth
EOF
openssl x509 -req -sha384 -in "$D/int.csr" -CA "$D/root-ca.pem" -CAkey "$D/root-ca-key.pem" \
  -CAcreateserial -out "$D/intermediate-ca.pem" -days 1825 -extfile "$D/_ext.cnf" 2>/dev/null
cat "$D/intermediate-ca.pem" "$D/root-ca.pem" > "$D/ca-chain.pem"
echo "  ✓ intermediate-ca.pem + ca-chain.pem"

echo -e "\n[3/4] Server TLS cert..."
openssl ecparam -genkey -name prime256v1 -noout -out "$D/server-key.pem" 2>/dev/null
cat > "$D/_ext.cnf" <<EOF
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,DNS:edgeflux-server,DNS:gateway.edgeflux.local,DNS:mosquitto,IP:127.0.0.1
EOF
openssl req -new -sha256 -key "$D/server-key.pem" -out "$D/srv.csr" \
  -subj "/C=GB/O=EdgeFlux Systems/CN=gateway.edgeflux.local" 2>/dev/null
openssl x509 -req -sha256 -in "$D/srv.csr" -CA "$D/intermediate-ca.pem" -CAkey "$D/intermediate-ca-key.pem" \
  -CAcreateserial -out "$D/server-cert.pem" -days 365 -extfile "$D/_ext.cnf" 2>/dev/null
cat "$D/server-cert.pem" "$D/intermediate-ca.pem" > "$D/server.pem"
echo "  ✓ server.pem + server-key.pem"

echo -e "\n[4/4] Bootstrap client cert (24hr)..."
openssl ecparam -genkey -name prime256v1 -noout -out "$D/bootstrap-key.pem" 2>/dev/null
cat > "$D/_ext.cnf" <<EOF
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EOF
openssl req -new -sha256 -key "$D/bootstrap-key.pem" -out "$D/bs.csr" \
  -subj "/C=GB/O=EdgeFlux Systems/OU=Bootstrap/CN=bootstrap.agent" 2>/dev/null
openssl x509 -req -sha256 -in "$D/bs.csr" -CA "$D/intermediate-ca.pem" -CAkey "$D/intermediate-ca-key.pem" \
  -CAcreateserial -out "$D/bootstrap.pem" -days 1 -extfile "$D/_ext.cnf" 2>/dev/null
echo "  ✓ bootstrap.pem (24hr expiry)"

rm -f "$D"/*.csr "$D"/_ext.cnf "$D"/*.srl
echo -e "\n══════════════════════════════════════════"
echo " PKI complete: $(ls "$D"/*.pem | wc -l) files"
ls -1 "$D"/*.pem | sed 's/^/  /'
echo "══════════════════════════════════════════"
