#!/bin/bash

mkdir -vp $(pwd)/ssl
mkdir -vp $(pwd)/keys
mkdir -vp $(pwd)/pubkeys
mkdir -vp $(pwd)/users
mkdir -vp $(pwd)/headers
mkdir -vp $(pwd)/fwlogs
mkdir -vp $(pwd)/census

docker stop sish
docker rm sish

docker run -itd \
  --name=sish \
  --hostname "sish.noc" \
  --pull=always \
  -v $(pwd)/ssl:/ssl \
  -v $(pwd)/keys:/keys \
  -v $(pwd)/pubkeys:/pubkeys \
  -v $(pwd)/users:/users \
  -v $(pwd)/headers:/headers \
  -v $(pwd)/fwlogs:/fwlogs \
  -v $(pwd)/census:/census \
  -v /home/admin/sish/certs/certificate:/certificates \
  --restart=always \
  -p 2222:2222 \
  -p 80:80 -p 443:443 \
  -p 9000-9010:9000-9010/tcp \
  ghcr.io/manprint/sish:v2.23.0 \
  --ssh-address=:2222 \
  --ssh-over-https \
  --http-address=:80 \
  --https-address=:443 \
  --https=true \
  --force-https=true \
  --https-certificate-directory=/ssl \
  --authentication-keys-directory=/pubkeys \
  --private-keys-directory=/keys \
  --bind-random-aliases=false \
  --bind-random-ports=false \
  --bind-random-subdomains=false \
  --force-requested-aliases=false \
  --force-requested-ports=false \
  --force-requested-subdomains=false \
  --authentication-password="mysecretpassword" \
  --auth-users-enabled=true \
  --auth-users-directory=/users \
  --headers-setting-directory=/headers \
  --headers-managed=true \
  --cleanup-unbound=false \
  --idle-connection=false \
  --ping-client=false \
  --admin-console \
  --census-enabled=true \
  --strict-id-censed-url=true \
  --strict-id-censed-files=true \
  --census-url="https://myurl.com" \
  --census-directory=/census \
  --census-refresh-time=30s \
  --admin-console-token="mysecrettoken" \
  --admin-consolle-editkeys-credentials="admin:mysecretpassword" \
  --admin-consolle-editusers-credentials="admin:mysecretpassword" \
  --admin-consolle-editheaders-credentials="admin:mysecretpassword" \
  --admin-consolle-editcensus-credentials="admin:mysecretpassword" \
  --user-bandwidth-limiter-enabled=true \
  --service-console-max-content-length=0 \
  --proxy-protocol-listener=true \
  --history-enabled=true \
  --log-to-client \
  --verify-ssl=false \
  --domain=sish.mydomain.com \
  --enable-force-connect \
  --show-internal-state=true \
  --forwarders-log=enable \
  --forwarders-log-dir=/fwlogs \
  --forwarders-log-max-size=20 \
  --forwarders-log-max-backups=5 \
  --forwarders-log-max-age=60 \
  --forwarders-log-compress=true