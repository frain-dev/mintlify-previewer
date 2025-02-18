# Dockerfile for Mintlify Previewer Backend
# Builds and runs the Mintlify preview service inside a container

# LABEL description="Mintlify Previewer Backend for GitHub Actions"
# LABEL version="0.0.1"

FROM golang:1.23.6-alpine AS builder

WORKDIR /app

# Install required dependencies for CGO
RUN apk add --no-cache gcc musl-dev

# Enable CGO
ENV CGO_ENABLED=1

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# Build with CGO enabled
RUN go build -o server .

# Runtime stage
FROM alpine:latest

WORKDIR /root/

RUN apk add --no-cache git nodejs npm

RUN npm install -g mintlify

COPY --from=builder /app/server .

COPY static/ static/
COPY migrations/ migrations/

EXPOSE 8080

CMD ["./server"]
