# Use last Golang runtime
FROM golang:latest

# Set the working directory to /go/src/app
WORKDIR /go/src/app

# Copy the current directory contents into the container at /go/src/app
COPY . .

# Build the executable
RUN go build -o main .

# Expose port 8080 for the bot to listen on
EXPOSE 8080

# Run the bot executable by default when the container starts
CMD ["./main"]
