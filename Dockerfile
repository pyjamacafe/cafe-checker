FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -o cafe-checker .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    g++ \
    python3 \
    binutils \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /src/cafe-checker /app/cafe-checker
RUN mkdir -p /tmp/judge && chmod 777 /tmp/judge
RUN useradd -m judge
# Remove escalation tools — su, mount, umount, and all setuid/setgid bits
RUN rm -f /usr/bin/su /usr/bin/newgrp /usr/bin/chsh /usr/bin/chfn /usr/bin/mount /usr/bin/umount \
        /usr/sbin/pwconv /usr/sbin/pwunconv /usr/sbin/chpasswd 2>/dev/null; \
    find / -perm /6000 -type f -exec chmod u-s,g-s {} + 2>/dev/null || true
USER judge
EXPOSE 4000
CMD ["/app/cafe-checker"]
