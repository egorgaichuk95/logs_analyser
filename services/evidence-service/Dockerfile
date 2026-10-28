FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.work ./
COPY pkg/contracts ./pkg/contracts
COPY services/evidence-service ./services/evidence-service
COPY services/ingest-service/go.mod ./services/ingest-service/go.mod
COPY services/rca-engine/go.mod ./services/rca-engine/go.mod
COPY services/gateway-api/go.mod ./services/gateway-api/go.mod
COPY services/tilt-collector/go.mod ./services/tilt-collector/go.mod
RUN cd services/evidence-service && CGO_ENABLED=0 go build -o /out/evidence ./cmd/evidence

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY --from=build /out/evidence /app/evidence
EXPOSE 8083
CMD ["/app/evidence"]
