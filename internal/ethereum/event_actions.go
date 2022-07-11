// Copyright © 2022 Kaleido, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ethereum

import (
	"context"
	"encoding/json"

	"github.com/hyperledger/firefly-common/pkg/fftypes"
	"github.com/hyperledger/firefly-common/pkg/i18n"
	"github.com/hyperledger/firefly-evmconnect/internal/msgs"
	"github.com/hyperledger/firefly-transaction-manager/pkg/ffcapi"
)

func (c *ethConnector) EventStreamStart(ctx context.Context, req *ffcapi.EventStreamStartRequest) (*ffcapi.EventStreamStartResponse, ffcapi.ErrorReason, error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	es := c.eventStreams[*req.ID]
	if es != nil {
		return nil, ffcapi.ErrorReason(""), i18n.NewError(ctx, msgs.MsgStreamAlreadyStarted, req.ID)
	}

	es = &eventStream{
		id:             req.ID,
		c:              c,
		ctx:            req.StreamContext,
		events:         req.EventStream,
		headBlock:      -1,
		listeners:      make(map[fftypes.UUID]*listener),
		streamLoopDone: make(chan struct{}),
	}
	for _, lReq := range req.InitialListeners {
		l, err := es.addEventListener(ctx, lReq)
		if err != nil {
			return nil, "", err
		}
		// During initial start we move the "head" block forwards to be the highest of all the initial streams
		if l.hwmBlock > es.headBlock {
			es.headBlock = l.hwmBlock
		}
	}

	// From this point we consider ourselves started
	c.eventStreams[*req.ID] = es

	// Now we've calculated our head block, go through and start all the listeners - which might kick off catchup on some of them
	for _, l := range es.listeners {
		l.start(ctx)
	}

	// Start the listener head routine, which reads events for all listeners that are not in catchup mode
	go es.streamLoop()

	// Add the block consumer
	c.blockListener.addConsumer(&blockUpdateConsumer{
		id:      es.id,
		ctx:     req.StreamContext,
		updates: req.BlockListener,
	})

	return &ffcapi.EventStreamStartResponse{}, "", nil
}

func (c *ethConnector) EventStreamStopped(ctx context.Context, req *ffcapi.EventStreamStoppedRequest) (*ffcapi.EventStreamStoppedResponse, ffcapi.ErrorReason, error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	es := c.eventStreams[*req.ID]
	if es != nil {
		select {
		case <-es.ctx.Done():
			// This is good, it is stopped
		default:
			return nil, ffcapi.ErrorReason(""), i18n.NewError(ctx, msgs.MsgStreamNotStopped, req.ID)
		}
		// Wait for stream loop to complete
		<-es.streamLoopDone
	}
	delete(c.eventStreams, *req.ID)
	return &ffcapi.EventStreamStoppedResponse{}, "", nil
}

func (c *ethConnector) EventListenerVerifyOptions(ctx context.Context, req *ffcapi.EventListenerVerifyOptionsRequest) (*ffcapi.EventListenerVerifyOptionsResponse, ffcapi.ErrorReason, error) {

	signature, _, err := parseEventFilters(ctx, req.Filters)
	if err != nil {
		return nil, "", err
	}

	options, err := parseListenerOptions(ctx, req.Options)
	if err != nil {
		return nil, "", err
	}

	ob, _ := json.Marshal(&options)
	return &ffcapi.EventListenerVerifyOptionsResponse{
		ResolvedSignature: signature,
		ResolvedOptions:   fftypes.JSONAny(ob),
	}, "", nil

}

func (c *ethConnector) EventListenerAdd(ctx context.Context, req *ffcapi.EventListenerAddRequest) (*ffcapi.EventListenerAddResponse, ffcapi.ErrorReason, error) {
	c.mux.Lock()
	es := c.eventStreams[*req.StreamID]
	c.mux.Unlock()
	if es == nil {
		return nil, ffcapi.ErrorReasonNotFound, i18n.NewError(ctx, msgs.MsgStreamNotStarted, req.StreamID)
	}
	l, err := es.addEventListener(ctx, req)
	if err != nil {
		return nil, ffcapi.ErrorReason(""), err
	}
	// We start this listener straight away
	l.start(ctx)
	return &ffcapi.EventListenerAddResponse{}, ffcapi.ErrorReason(""), nil
}

func (c *ethConnector) EventListenerRemove(ctx context.Context, req *ffcapi.EventListenerRemoveRequest) (*ffcapi.EventListenerRemoveResponse, ffcapi.ErrorReason, error) {
	c.mux.Lock()
	es := c.eventStreams[*req.StreamID]
	c.mux.Unlock()
	if es == nil {
		return nil, ffcapi.ErrorReasonNotFound, i18n.NewError(ctx, msgs.MsgStreamNotStarted, req.StreamID)
	}
	es.removeEventListener(req.ListenerID)
	return &ffcapi.EventListenerRemoveResponse{}, ffcapi.ErrorReason(""), nil
}

func (c *ethConnector) EventStreamNewCheckpointStruct() ffcapi.EventListenerCheckpoint {
	return &listenerCheckpoint{}
}

func (c *ethConnector) EventListenerHWM(ctx context.Context, req *ffcapi.EventListenerHWMRequest) (*ffcapi.EventListenerHWMResponse, ffcapi.ErrorReason, error) {
	c.mux.Lock()
	es := c.eventStreams[*req.StreamID]
	c.mux.Unlock()
	if es == nil {
		return nil, ffcapi.ErrorReasonNotFound, i18n.NewError(ctx, msgs.MsgStreamNotStarted, req.StreamID)
	}
	return es.getListenerHWM(ctx, req.ListenerID)
}
