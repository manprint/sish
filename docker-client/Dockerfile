FROM alpine:3.23

RUN apk add --no-cache \
    openssh-client \
    sshpass \
    su-exec \
    ca-certificates \
    tzdata \
    bash

WORKDIR /app

COPY docker-client/entrypoint.sh /app/entrypoint.sh
COPY docker-client/scripts/ /app/scripts/

RUN chmod +x /app/entrypoint.sh /app/scripts/*.sh && \
    mkdir -p /app/log/forwarder /app/log/stats /app/ssh

VOLUME ["/app/log", "/app/ssh"]

ENTRYPOINT ["/app/entrypoint.sh"]
