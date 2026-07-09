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
USER judge
EXPOSE 4000
CMD ["/app/cafe-checker"]
