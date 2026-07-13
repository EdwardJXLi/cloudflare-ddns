package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cloudflare-ddns/internal/agent"
	"cloudflare-ddns/internal/cloudflare"
	"cloudflare-ddns/internal/hub"
	"cloudflare-ddns/internal/store"
)

const defaultDataFile = "/data/clients.json"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a command is required")
	}

	switch args[0] {
	case "hub":
		return runHub()
	case "agent":
		return runAgent()
	case "healthcheck":
		return runHealthcheck()
	case "clients":
		return runClients(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runHealthcheck() error {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(envDefault("HEALTHCHECK_URL", "http://127.0.0.1:8080/healthz"))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `Cloudflare DDNS

Usage:
  cloudflare-ddns COMMAND

Commands:
  hub                  Run the central service that updates Cloudflare DNS records.
  agent                Run an agent that reports its public IPv4 address to the hub.
  healthcheck          Check whether the configured hub health endpoint is available.
  clients add NAME     Create a client and print its new token.
  clients list         List all configured client names.
  clients rotate NAME  Replace a client's token and print the new one.
  clients remove NAME  Remove a client and revoke its token.

Examples:
  cloudflare-ddns hub
  cloudflare-ddns agent
  cloudflare-ddns clients add home-server
  cloudflare-ddns clients list
  cloudflare-ddns clients rotate home-server
  cloudflare-ddns clients remove home-server`)
}

func runHub() error {
	token := strings.TrimSpace(os.Getenv("CF_API_TOKEN"))
	if token == "" {
		return errors.New("CF_API_TOKEN is required")
	}

	zone := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(os.Getenv("ZONE")), "."))
	if err := validateHostname(zone); err != nil {
		return fmt.Errorf("ZONE: %w", err)
	}

	dataFile := envDefault("DATA_FILE", defaultDataFile)
	credentials := store.New(dataFile)
	if err := credentials.Initialize(); err != nil {
		return err
	}

	logger := newLogger()
	cloudflareClient := cloudflare.New(token)

	lookupContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	zoneID, err := cloudflareClient.ResolveZone(lookupContext, zone)
	if err != nil {
		return err
	}

	service := hub.New(credentials, cloudflareClient, zoneID, zone, logger)
	server := &http.Server{
		Addr:              envDefault("LISTEN_ADDR", ":8080"),
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()

		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		server.Shutdown(shutdown) //nolint:errcheck
	}()

	logger.Info("hub listening", "address", server.Addr, "zone", zone, "data_file", dataFile)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func runAgent() error {
	token := strings.TrimSpace(os.Getenv("CLIENT_TOKEN"))
	interval, err := durationEnv("UPDATE_INTERVAL", 5*time.Minute)
	if err != nil {
		return err
	}
	allowInsecureHTTP, err := boolEnv("ALLOW_INSECURE_HTTP")
	if err != nil {
		return err
	}

	config := agent.Config{
		HubURL:            os.Getenv("HUB_URL"),
		Token:             token,
		IPv4Provider:      envDefault("IPV4_PROVIDER", "https://api.ipify.org"),
		Interval:          interval,
		AllowInsecureHTTP: allowInsecureHTTP,
	}

	runner, err := agent.New(config, newLogger())
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return runner.Run(ctx)
}

func runClients(args []string) error {
	if len(args) == 0 {
		return errors.New("clients requires add, list, rotate, or remove")
	}
	action := args[0]
	if action != "add" && action != "list" && action != "rotate" && action != "remove" {
		return fmt.Errorf("unknown clients command %q", action)
	}

	flags := flag.NewFlagSet("clients "+action, flag.ContinueOnError)
	dataFile := flags.String("data-file", envDefault("DATA_FILE", defaultDataFile), "credential database path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	credentials := store.New(*dataFile)

	if action == "list" {
		if flags.NArg() != 0 {
			return errors.New("usage: clients list")
		}
		clients, err := credentials.List()
		if err != nil {
			return err
		}
		for _, client := range clients {
			fmt.Println(client.ID)
		}

		return nil
	}

	if flags.NArg() != 1 {
		return fmt.Errorf("usage: clients %s NAME", action)
	}
	name := strings.ToLower(flags.Arg(0))
	if err := validateClientName(name); err != nil {
		return err
	}

	switch action {
	case "add":
		token, err := credentials.Add(name)
		if err != nil {
			return err
		}
		fmt.Println(token)
		fmt.Fprintln(os.Stderr, "The client token is printed above once. Store it on the client, then clear it from shell history if necessary.")
	case "rotate":
		token, err := credentials.Rotate(name)
		if err != nil {
			return err
		}
		fmt.Println(token)
		fmt.Fprintln(os.Stderr, "The previous token has been revoked. This replacement is shown once.")
	case "remove":
		if err := credentials.Remove(name); err != nil {
			return err
		}

		fmt.Printf("removed %s\n", name)
	}

	return nil
}

func validateClientName(name string) error {
	if strings.Contains(name, ".") || validateHostname(name) != nil {
		return errors.New("name must be one DNS label containing only letters, numbers, or hyphens")
	}

	return nil
}

func validateHostname(hostname string) error {
	if len(hostname) == 0 || len(hostname) > 253 {
		return errors.New("hostname must be between 1 and 253 characters")
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid hostname %q", hostname)
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return fmt.Errorf("invalid hostname %q; use its ASCII/Punycode form", hostname)
			}
		}
	}

	return nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 5m", name)
	}

	return duration, nil
}

func boolEnv(name string) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return false, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean such as true or false", name)
	}

	return parsed, nil
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
