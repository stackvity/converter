# --- START OF REVISED FILE build/Dockerfile ---
# Dockerfile for building and running stack-converter
# Ref: techstack.md#7.B
# (No changes needed)

# --- Build Stage ---
# Use an official Go image as the build environment
# Use the Go version specified in go.mod
FROM golang:1.24-alpine AS builder

# Set working directory
WORKDIR /app

# Copy Go module files and download dependencies first to leverage Docker layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire source code
COPY . .

# Build the application with ldflags for version injection and optimization
# Requires ARG values to be passed during build time (e.g., via goreleaser or docker build --build-arg)
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN go build -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /stack-converter ./cmd/stack-converter

# --- Final Stage ---
# Use a minimal base image like Alpine for the final image
FROM alpine:latest

# Alpine needs certificates and timezone data
RUN apk add --no-cache ca-certificates tzdata

# Copy only the compiled binary from the builder stage
COPY --from=builder /stack-converter /usr/local/bin/stack-converter

# Optional: Add git if the os/exec GitClient implementation is the primary one expected in container
# RUN apk add --no-cache git

# Optional: Add other runtimes if plugins require them (e.g., Python, Node.js)
# Recommendation 3: Add comment clarifying user responsibility for plugin runtimes
# IMPORTANT: Uncomment and add runtimes below ONLY if you intend to use plugins
# requiring them *within this container*. stack-converter itself does not require them.
# RUN apk add --no-cache python3 nodejs

# Set the entrypoint to the application binary
ENTRYPOINT ["stack-converter"]

# Set the default command (e.g., show help)
CMD ["--help"]

# --- END OF REVISED FILE build/Dockerfile ---