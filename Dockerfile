# Stage 1: Build the Go application
FROM golang:latest
LABEL authors="the-eduardo"

# Set the working directory to /go/src/app
WORKDIR /app

# Copy just the Go module files first for improved caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . ./

# Build the application
RUN CGO_ENABLED=0 GOARCH=arm64 GOOS=linux go build -o app

# Todo: Add another minimal runtime image to reduce the size ~ 1 GB
# Expose port 8080 for the bot to listen on
EXPOSE 8080

# Set the entry point to run the application
CMD ["./app"]