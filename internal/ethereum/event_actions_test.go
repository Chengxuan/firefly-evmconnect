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
	"testing"

	"github.com/hyperledger/firefly-common/pkg/fftypes"
	"github.com/hyperledger/firefly-evmconnect/mocks/jsonrpcmocks"
	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"github.com/hyperledger/firefly-transaction-manager/pkg/ffcapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const abiTransferEvent = `{
	"anonymous": false,
	"inputs": [
		{
			"indexed": true,
			"name": "from",
			"type": "address"
		},
		{
			"indexed": true,
			"name": "to",
			"type": "address"
		},
		{
			"indexed": false,
			"name": "value",
			"type": "uint256"
		}
	],
	"name": "Transfer",
	"type": "event"
}`

const abiTransferFn = `{
	"constant": false,
	"inputs": [
		{
			"name": "_to",
			"type": "address"
		},
		{
			"name": "_value",
			"type": "uint256"
		}
	],
	"name": "transfer",
	"outputs": [
		{
			"name": "",
			"type": "bool"
		}
	],
	"payable": false,
	"stateMutability": "nonpayable",
	"type": "function"
}`

func mockStreamLoopEmpty(mRPC *jsonrpcmocks.Client) {
	mRPC.On("Invoke", mock.Anything, mock.Anything, "eth_blockNumber").Return(nil).Run(func(args mock.Arguments) {
		hbh := args[1].(*ethtypes.HexInteger)
		*hbh = *ethtypes.NewHexInteger64(12345)
	})
	mRPC.On("Invoke", mock.Anything, mock.Anything, "eth_newFilter", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		hbh := args[1].(**ethtypes.HexInteger)
		*hbh = ethtypes.NewHexInteger64(101010)
	})
	mRPC.On("Invoke", mock.Anything, mock.Anything, "eth_getFilterLogs", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		*args[1].(*[]*logJSONRPC) = make([]*logJSONRPC, 0)
	})
	mRPC.On("Invoke", mock.Anything, mock.Anything, "eth_getFilterChanges", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		*args[1].(*[]*logJSONRPC) = make([]*logJSONRPC, 0)
	}).Maybe()
}

func TestEventStreamStartStopOk(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	sContext, sCancel := context.WithCancel(context.Background())
	sChannel := make(chan *ffcapi.ListenerEvent)
	sBlocks := make(chan *ffcapi.BlockHashEvent)

	sID := fftypes.NewUUID()
	lID := fftypes.NewUUID()
	startRequest := &ffcapi.EventStreamStartRequest{
		ID: sID,
		InitialListeners: []*ffcapi.EventListenerAddRequest{
			{
				StreamID:   sID,
				ListenerID: lID,
				Name:       "listener1",
				EventListenerOptions: ffcapi.EventListenerOptions{
					FromBlock: "0",
					Filters: []fftypes.JSONAny{*fftypes.JSONAnyPtr(`{
						"address": "0x5600fF383458ae30dE902D096bA89f7F81f0a2fC",
						"event": ` + abiTransferEvent + `
					}`)},
					Options: fftypes.JSONAnyPtr(`{
						"methods": [` + abiTransferFn + `]
					}`),
				},
				Checkpoint: &listenerCheckpoint{
					Block:            12345, // No catchup required
					TransactionIndex: 123,
					LogIndex:         0,
				},
			},
		},
		StreamContext: sContext,
		EventStream:   sChannel,
		BlockListener: sBlocks,
	}

	r1, _, err := c.EventStreamStart(ctx, startRequest)
	assert.NoError(t, err)
	assert.NotNil(t, r1)

	// Check double-start rejected
	_, _, err = c.EventStreamStart(ctx, startRequest)
	assert.Regexp(t, "FF23042", err)

	r2, _, err := c.EventListenerHWM(ctx, &ffcapi.EventListenerHWMRequest{
		StreamID:   sID,
		ListenerID: lID,
	})
	assert.NoError(t, err)
	assert.Equal(t, int64(12345), r2.Checkpoint.(*listenerCheckpoint).Block)

	_, _, err = c.EventStreamStopped(ctx, &ffcapi.EventStreamStoppedRequest{
		ID: sID,
	})
	assert.Regexp(t, "FF23045", err)

	sCancel()

	r3, _, err := c.EventStreamStopped(ctx, &ffcapi.EventStreamStoppedRequest{
		ID: sID,
	})
	assert.NoError(t, err)
	assert.NotNil(t, r3)

}

func TestEventStreamStartBadListener(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	sChannel := make(chan *ffcapi.ListenerEvent)
	sBlocks := make(chan *ffcapi.BlockHashEvent)

	sID := fftypes.NewUUID()
	lID := fftypes.NewUUID()
	startRequest := &ffcapi.EventStreamStartRequest{
		ID: sID,
		InitialListeners: []*ffcapi.EventListenerAddRequest{
			{
				StreamID:   sID,
				ListenerID: lID,
				Name:       "listener1",
				EventListenerOptions: ffcapi.EventListenerOptions{
					FromBlock: "0",
					Filters:   []fftypes.JSONAny{*fftypes.JSONAnyPtr(`{"bad JSON!"`)},
				},
			},
		},
		StreamContext: ctx,
		EventStream:   sChannel,
		BlockListener: sBlocks,
	}

	_, _, err := c.EventStreamStart(ctx, startRequest)
	assert.Regexp(t, "FF23033", err)

}

func TestEventListenerVerifyOptionsOk(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	res, _, err := c.EventListenerVerifyOptions(ctx, &ffcapi.EventListenerVerifyOptionsRequest{
		EventListenerOptions: ffcapi.EventListenerOptions{
			FromBlock: "12345",
			Filters: []fftypes.JSONAny{*fftypes.JSONAnyPtr(`{
				"address": "0x5600fF383458ae30dE902D096bA89f7F81f0a2fC",
				"event": ` + abiTransferEvent + `
			}`)},
			Options: fftypes.JSONAnyPtr(`{
				"methods": [` + abiTransferFn + `]
			}`),
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, `{"methods":[{"type":"function","name":"transfer","stateMutability":"nonpayable","inputs":[{"name":"_to","type":"address"},{"name":"_value","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}]}`, string(res.ResolvedOptions))
	assert.Equal(t, `0x5600ff383458ae30de902d096ba89f7f81f0a2fc:Transfer(address,address,uint256)`, res.ResolvedSignature)

}

func TestEventListenerVerifyOptionsBadFilters(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	_, _, err := c.EventListenerVerifyOptions(ctx, &ffcapi.EventListenerVerifyOptionsRequest{
		EventListenerOptions: ffcapi.EventListenerOptions{
			FromBlock: "12345",
			Filters:   []fftypes.JSONAny{*fftypes.JSONAnyPtr(`{"Bad JSON!`)},
			Options: fftypes.JSONAnyPtr(`{
				"methods": [` + abiTransferFn + `]
			}`),
		},
	})
	assert.Regexp(t, "FF23036", err)

}

func TestEventListenerVerifyOptionsBadOptions(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	_, _, err := c.EventListenerVerifyOptions(ctx, &ffcapi.EventListenerVerifyOptionsRequest{
		EventListenerOptions: ffcapi.EventListenerOptions{
			FromBlock: "12345",
			Filters: []fftypes.JSONAny{*fftypes.JSONAnyPtr(`{
				"event": ` + abiTransferEvent + `
			}`)},
			Options: fftypes.JSONAnyPtr(`{"Bad JSON!`),
		},
	})
	assert.Regexp(t, "FF23033", err)

}

func TestEventStreamAddRemoveOk(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	sContext, sCancel := context.WithCancel(context.Background())
	sChannel := make(chan *ffcapi.ListenerEvent)
	sBlocks := make(chan *ffcapi.BlockHashEvent)

	sID := fftypes.NewUUID()
	lID := fftypes.NewUUID()
	startRequest := &ffcapi.EventStreamStartRequest{
		ID:            sID,
		StreamContext: sContext,
		EventStream:   sChannel,
		BlockListener: sBlocks,
	}

	r1, _, err := c.EventStreamStart(ctx, startRequest)
	assert.NoError(t, err)
	assert.NotNil(t, r1)

	r2, _, err := c.EventListenerAdd(ctx, &ffcapi.EventListenerAddRequest{
		StreamID:   sID,
		ListenerID: lID,
		Name:       "listener1",
		EventListenerOptions: ffcapi.EventListenerOptions{
			FromBlock: "0",
			Filters: []fftypes.JSONAny{*fftypes.JSONAnyPtr(`{
				"address": "0x5600fF383458ae30dE902D096bA89f7F81f0a2fC",
				"event": ` + abiTransferEvent + `
			}`)},
			Options: fftypes.JSONAnyPtr(`{
				"methods": [` + abiTransferFn + `]
			}`),
		},
		Checkpoint: &listenerCheckpoint{
			Block:            12345, // No catchup required
			TransactionIndex: 123,
			LogIndex:         0,
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, r2)

	r3, _, err := c.EventListenerRemove(ctx, &ffcapi.EventListenerRemoveRequest{
		StreamID:   sID,
		ListenerID: lID,
	})
	assert.NoError(t, err)
	assert.NotNil(t, r3)

	sCancel()

	r4, _, err := c.EventStreamStopped(ctx, &ffcapi.EventStreamStoppedRequest{
		ID: sID,
	})
	assert.NoError(t, err)
	assert.NotNil(t, r4)

}

func TestEventStreamAddBadOptions(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	sContext, sCancel := context.WithCancel(context.Background())
	sChannel := make(chan *ffcapi.ListenerEvent)
	sBlocks := make(chan *ffcapi.BlockHashEvent)

	sID := fftypes.NewUUID()
	lID := fftypes.NewUUID()
	startRequest := &ffcapi.EventStreamStartRequest{
		ID:            sID,
		StreamContext: sContext,
		EventStream:   sChannel,
		BlockListener: sBlocks,
	}

	r1, _, err := c.EventStreamStart(ctx, startRequest)
	assert.NoError(t, err)
	assert.NotNil(t, r1)

	_, _, err = c.EventListenerAdd(ctx, &ffcapi.EventListenerAddRequest{
		StreamID:   sID,
		ListenerID: lID,
		Name:       "listener1",
		EventListenerOptions: ffcapi.EventListenerOptions{
			FromBlock: "0",
			Filters:   []fftypes.JSONAny{*fftypes.JSONAnyPtr(`{"Bad JSON!`)},
		},
	})
	assert.Regexp(t, "FF23033", err)

	sCancel()

	r3, _, err := c.EventStreamStopped(ctx, &ffcapi.EventStreamStoppedRequest{
		ID: sID,
	})
	assert.NoError(t, err)
	assert.NotNil(t, r3)

}

func TestEventStreamAddBadStream(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	sID := fftypes.NewUUID()
	lID := fftypes.NewUUID()
	_, _, err := c.EventListenerAdd(ctx, &ffcapi.EventListenerAddRequest{
		StreamID:   sID,
		ListenerID: lID,
		Name:       "listener1",
	})
	assert.Regexp(t, "FF23041", err)

}

func TestEventStreamRemoveBadStream(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	sID := fftypes.NewUUID()
	lID := fftypes.NewUUID()
	_, _, err := c.EventListenerRemove(ctx, &ffcapi.EventListenerRemoveRequest{
		StreamID:   sID,
		ListenerID: lID,
	})
	assert.Regexp(t, "FF23041", err)

}

func TestEventStreamNewCheckpointStruct(t *testing.T) {

	_, done, c, _ := newTestConnector(t)
	defer done()

	var expectedType *listenerCheckpoint
	assert.IsType(t, expectedType, c.EventStreamNewCheckpointStruct())

}

func TestEventListenerHWMBadStream(t *testing.T) {

	ctx, done, c, mRPC := newTestConnector(t)
	defer done()
	mockStreamLoopEmpty(mRPC)

	sID := fftypes.NewUUID()
	lID := fftypes.NewUUID()
	_, _, err := c.EventListenerHWM(ctx, &ffcapi.EventListenerHWMRequest{
		StreamID:   sID,
		ListenerID: lID,
	})
	assert.Regexp(t, "FF23041", err)

}
