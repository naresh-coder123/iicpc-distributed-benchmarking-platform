package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	tradingpb "github.com/iicpc/platform/gen/go/iicpc/trading"
	"google.golang.org/grpc"
)

type server struct {
	tradingpb.UnimplementedTradingGatewayServer
	rng *rand.Rand
}

func (s *server) PlaceOrder(ctx context.Context, req *tradingpb.OrderRequest) (*tradingpb.OrderResponse, error) {
	// Phase 1 mock: simulate small, bounded processing delay (1–15ms).
	delay := time.Duration(1+s.rng.Intn(15)) * time.Millisecond
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return nil, ctx.Err()
	case <-timer.C:
	}

	// Minimal validation.
	if req.GetOrderId() == "" || req.GetClientId() == "" || req.GetSymbol() == "" || req.GetQuantity() == 0 {
		return &tradingpb.OrderResponse{
			OrderId:        req.GetOrderId(),
			ClientId:       req.GetClientId(),
			Symbol:         req.GetSymbol(),
			Status:         "REJECTED",
			ExecutionPrice: 0,
			FilledQuantity: 0,
			TimestampNs:    uint64(time.Now().UTC().UnixNano()),
		}, nil
	}

	var execPrice float64
	if req.GetOrderType() == tradingpb.OrderType_LIMIT {
		execPrice = req.GetPrice()
	} else {
		// Market: synthetic mid around 100.0
		execPrice = 100.0 + (s.rng.Float64()*0.5 - 0.25)
	}

	return &tradingpb.OrderResponse{
		OrderId:        req.GetOrderId(),
		ClientId:       req.GetClientId(),
		Symbol:         req.GetSymbol(),
		Status:         "FILLED",
		ExecutionPrice: execPrice,
		FilledQuantity: req.GetQuantity(),
		TimestampNs:    uint64(time.Now().UTC().UnixNano()),
	}, nil
}

func (s *server) StreamOrders(stream tradingpb.TradingGateway_StreamOrdersServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		resp, err := s.PlaceOrder(stream.Context(), req)
		if err != nil {
			return err
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

func main() {
	var addr string
	flag.StringVar(&addr, "addr", "0.0.0.0:50051", "listen address")
	flag.Parse()

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	tradingpb.RegisterTradingGatewayServer(grpcServer, &server{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	})

	fmt.Printf("Mock matcher (Go) listening on %s\n", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
