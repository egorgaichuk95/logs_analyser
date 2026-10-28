FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.work ./
COPY pkg/contracts ./pkg/contracts
COPY services/tilt-collector ./services/tilt-collector
COPY services/ingest-service/go.mod ./services/ingest-service/go.mod
COPY services/rca-engine/go.mod ./services/rca-engine/go.mod
COPY services/evidence-service/go.mod ./services/evidence-service/go.mod
COPY services/gateway-api/go.mod ./services/gateway-api/go.mod
RUN cd services/tilt-collector && CGO_ENABLED=0 go build -o /out/tiltcollector ./cmd/tiltcollector

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/tiltcollector /app/tiltcollector
EXPOSE 8084
CMD ["/app/tiltcollector"]
