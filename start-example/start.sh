#!/bin/bash

mkdir -vp $(pwd)/ssl
mkdir -vp $(pwd)/keys
mkdir -vp $(pwd)/pubkeys

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
  ghcr.io/manprint/sish:v2.24.2 \
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
  --authentication-password="C1YSEu0XIx66ejm7h96Du2r77Snpz7ap" \
  --auth-users-enabled=true \
  --auth-users-directory=/users \
  --headers-setting-directory=/headers \
  --headers-managed=true \
  --cleanup-unbound=false \
  --idle-connection=false \
  --ping-client=true \
  --ping-client-interval=5s \
  --ping-client-timeout=5m \
  --admin-console \
  --census-enabled=true \
  --strict-id-censed-url=true \
  --strict-id-censed-files=true \
  --strict-unique-ip=true \
  --census-url="https://pastebin.com/raw/awsedrftg" \
  --census-directory=/census \
  --census-refresh-time=10s \
  --admin-console-token="aaaaaaa" \
  --admin-consolle-editkeys-credentials="admin:aaaa" \
  --admin-consolle-editusers-credentials="admin:aaaa" \
  --admin-consolle-editheaders-credentials="admin:aaaa" \
  --admin-consolle-editcensus-credentials="admin:aaaa" \
  --user-bandwidth-limiter-enabled=true \
  --service-console-max-content-length=0 \
  --proxy-protocol-listener=true \
  --history-enabled=true \
  --log-to-client \
  --verify-ssl=false \
  --domain=tuns.0912345.xyz \
  --enable-force-connect \
  --show-internal-state=true \
  --forwarders-log=enable \
  --forwarders-log-dir=/fwlogs \
  --forwarders-log-max-size=20 \
  --forwarders-log-max-backups=5 \
  --forwarders-log-max-age=60 \
  --forwarders-log-compress=true \
  --bandwidth-hot-reload-enabled=true \
  --bandwidth-hot-reload-time=2s

# ghcr.io/manprint/sish:v2.24.2 (latest, in beta test)
#   --strict-id-censed=true \ (legacy)
