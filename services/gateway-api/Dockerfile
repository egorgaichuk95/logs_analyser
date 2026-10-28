FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.work ./
COPY pkg/contracts ./pkg/contracts
COPY services/gateway-api ./services/gateway-api
COPY services/ingest-service/go.mod ./services/ingest-service/go.mod
COPY services/rca-engine/go.mod ./services/rca-engine/go.mod
COPY services/evidence-service/go.mod ./services/evidence-service/go.mod
COPY services/tilt-collector/go.mod ./services/tilt-collector/go.mod
RUN cd services/gateway-api && CGO_ENABLED=0 go build -o /out/gateway ./cmd/gateway

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
EXPOSE 8080
CMD ["/app/gateway"]
