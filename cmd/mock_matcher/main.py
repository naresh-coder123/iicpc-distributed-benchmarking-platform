import asyncio
import os
import random
import sys
import time
from typing import AsyncIterator

import grpc


# Make local "pkg/" importable without requiring installation.
REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
PKG_DIR = os.path.join(REPO_ROOT, "pkg")
if PKG_DIR not in sys.path:
    sys.path.insert(0, PKG_DIR)

from iicpc.trading import trading_pb2, trading_pb2_grpc  # noqa: E402


class TradingGateway(trading_pb2_grpc.TradingGatewayServicer):
    async def PlaceOrder(
        self, request: trading_pb2.OrderRequest, context: grpc.aio.ServicerContext
    ) -> trading_pb2.OrderResponse:
        # Phase 1 mock: simulate a small, bounded processing delay.
        await asyncio.sleep(random.uniform(0.001, 0.015))

        # Minimal validation.
        if not request.order_id or not request.client_id or not request.symbol:
            return trading_pb2.OrderResponse(
                order_id=request.order_id,
                client_id=request.client_id,
                symbol=request.symbol,
                status="REJECTED",
                execution_price=0.0,
                filled_quantity=0,
                timestamp_ns=time.time_ns(),
            )

        if request.quantity == 0:
            return trading_pb2.OrderResponse(
                order_id=request.order_id,
                client_id=request.client_id,
                symbol=request.symbol,
                status="REJECTED",
                execution_price=0.0,
                filled_quantity=0,
                timestamp_ns=time.time_ns(),
            )

        # Simple fill rule:
        # - LIMIT: execute at the provided price
        # - MARKET: execute at a synthetic mid price near 100.0
        if request.order_type == trading_pb2.LIMIT:
            exec_price = request.price
        else:
            exec_price = 100.0 + random.uniform(-0.25, 0.25)

        return trading_pb2.OrderResponse(
            order_id=request.order_id,
            client_id=request.client_id,
            symbol=request.symbol,
            status="FILLED",
            execution_price=exec_price,
            filled_quantity=request.quantity,
            timestamp_ns=time.time_ns(),
        )

    async def StreamOrders(
        self,
        request_iterator: AsyncIterator[trading_pb2.OrderRequest],
        context: grpc.aio.ServicerContext,
    ) -> AsyncIterator[trading_pb2.OrderResponse]:
        # Kept for Phase 2+; we implement a basic echo loop so clients can already test streaming.
        async for req in request_iterator:
            resp = await self.PlaceOrder(req, context)
            yield resp


async def serve() -> None:
    server = grpc.aio.server(options=[("grpc.so_reuseport", 0)])
    trading_pb2_grpc.add_TradingGatewayServicer_to_server(TradingGateway(), server)

    listen_addr = os.environ.get("MOCK_MATCHER_ADDR", "0.0.0.0:50051")
    server.add_insecure_port(listen_addr)

    print(f"Mock matcher listening on {listen_addr}")
    await server.start()
    await server.wait_for_termination()


def main() -> None:
    asyncio.run(serve())


if __name__ == "__main__":
    main()

