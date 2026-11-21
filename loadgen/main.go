// loadgen drives the normal API using deterministic public test wallets on Anvil.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

type options struct {
	API           string        `json:"api"`
	RPC           string        `json:"rpc"`
	Mode          string        `json:"mode"`
	Traders       int           `json:"traders"`
	Concurrency   int           `json:"concurrency"`
	Duration      time.Duration `json:"duration_ns"`
	Timeout       time.Duration `json:"request_timeout_ns"`
	Drain         time.Duration `json:"drain_timeout_ns"`
	Warmup        time.Duration `json:"warmup_ns"`
	Seed          int64         `json:"seed"`
	Requests      int           `json:"max_requests"`
	BuyPercent    int           `json:"buy_percent"`
	AcceptPercent int           `json:"accept_percent"`
	Min           string        `json:"min_quantity"`
	Max           string        `json:"max_quantity"`
	Output        string        `json:"-"`
	Prepare       bool          `json:"-"`
}

func main() {
	var o options
	flag.StringVar(&o.API, "api", "http://127.0.0.1:38080", "Local Go API")
	flag.StringVar(&o.RPC, "rpc", "http://127.0.0.1:38545", "Local Anvil RPC")
	flag.StringVar(&o.Mode, "mode", "quotes", "quotes, accept or settlement")
	flag.IntVar(&o.Traders, "traders", 16, "Number of independent wallets")
	flag.IntVar(&o.Concurrency, "concurrency", 8, "Maximum simultaneous operations")
	flag.DurationVar(&o.Duration, "duration", 5*time.Second, "Operation admission window")
	flag.DurationVar(&o.Timeout, "timeout", 5*time.Second, "HTTP/RPC request timeout")
	flag.DurationVar(&o.Drain, "drain", 60*time.Second, "Deadline for completing admitted operations")
	flag.DurationVar(&o.Warmup, "warmup", time.Second, "RFQ warmup before measurement")
	flag.Int64Var(&o.Seed, "seed", 42, "Wallet and request seed")
	flag.IntVar(&o.Requests, "requests", 10000, "Operation and sample limit per campaign")
	flag.IntVar(&o.BuyPercent, "buy-percent", 50, "Expected BUY percentage")
	flag.IntVar(&o.AcceptPercent, "accept-percent", 100, "Quote acceptance percentage outside quotes mode")
	flag.StringVar(&o.Min, "min-quantity", "0.01", "Minimum exact tBILL quantity")
	flag.StringVar(&o.Max, "max-quantity", "0.1", "Maximum exact tBILL quantity")
	flag.StringVar(&o.Output, "output", "", "JSON report excluding keys and signatures")
	flag.BoolVar(&o.Prepare, "prepare", false, "Fund wallets and prepare allowances outside measurement")
	flag.Parse()
	if err := o.validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client, err := newClient(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer client.close()
	if o.Prepare {
		if err = client.prepare(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Prepared %d traders on Anvil.\n", o.Traders)
		return
	}
	report := client.run(ctx)
	if o.Output != "" {
		raw, e := json.MarshalIndent(report, "", "  ")
		if e == nil {
			e = os.WriteFile(o.Output, append(raw, '\n'), 0600)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(raw))
	if !report.Invariants.Passed || report.Cancelled || report.Errors > 0 {
		os.Exit(1)
	}
}

func (o options) validate() error {
	if o.Mode != "quotes" && o.Mode != "accept" && o.Mode != "settlement" {
		return fmt.Errorf("invalid mode")
	}
	if o.Traders < 1 || o.Traders > 10000 || o.Concurrency < 1 || o.Concurrency > o.Traders || o.Requests < 1 || o.Requests > 1000000 {
		return fmt.Errorf("1 <= concurrency <= traders <= 10000 and 1 <= requests <= 1000000 required")
	}
	if o.Duration <= 0 || o.Timeout <= 0 || o.Drain <= 0 || o.Warmup < 0 {
		return fmt.Errorf("invalid durations")
	}
	if o.BuyPercent < 0 || o.BuyPercent > 100 || o.AcceptPercent < 0 || o.AcceptPercent > 100 {
		return fmt.Errorf("percentages outside [0,100]")
	}
	low, err := atoms(o.Min)
	if err != nil || !low.IsInt64() || low.Sign() <= 0 {
		return fmt.Errorf("invalid minimum quantity")
	}
	high, err := atoms(o.Max)
	if err != nil || !high.IsInt64() || high.Cmp(low) < 0 || high.Int64() == int64(^uint64(0)>>1) {
		return fmt.Errorf("invalid maximum quantity")
	}
	for _, endpoint := range []string{o.API, o.RPC} {
		if err := localURL(endpoint); err != nil {
			return err
		}
	}
	return nil
}
