package libp2pclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
)

var log = logging.Logger("libp2pclient")

// Client Libp2pclient is responsible for sending
// requests to other peers.
type Client struct {
	ctx  context.Context
	host host.Host
	self peer.ID

	sendersLock sync.Mutex
	peerSenders map[peer.ID]*peerMessageSender
	protocols   []protocol.ID
}

// DecodeResponseFunc is a function that is passed into this generic libp2p
// Client to decode a response message.  This is needed because the generic
// client cannot decode the response message since the message is of a type
// only know to a specific libp2p client using this generic client.
type DecodeResponseFunc func([]byte) error

// Timeout to wait for a response after a request is sent
var readMessageTimeout = 10 * time.Second

// ErrReadTimeout is an error that occurs when no message is read within the timeout period.
var ErrReadTimeout = fmt.Errorf("timed out reading response")

// NewClient creates a new libp2pclient Client
func NewClient(ctx context.Context, h host.Host, protoID protocol.ID, options ...ClientOption) (*Client, error) {
	var cfg clientConfig
	if err := cfg.apply(options...); err != nil {
		return nil, err
	}

	// Start a client
	return &Client{
		ctx:         ctx,
		host:        h,
		self:        h.ID(),
		peerSenders: make(map[peer.ID]*peerMessageSender),
		protocols:   []protocol.ID{protoID},
	}, nil
}

// SendRequest sends out a request
func (c *Client) SendRequest(ctx context.Context, p peer.ID, msg proto.Message, decodeRsp DecodeResponseFunc) error {
	sender, err := c.messageSenderForPeer(ctx, p)
	if err != nil {
		log.Debugw("request failed to open message sender", "error", err, "to", p)
		return err
	}

	return sender.sendRequest(ctx, msg, decodeRsp, c.host, c.protocols)
}

// SendMessage sends out a message
func (c *Client) SendMessage(ctx context.Context, p peer.ID, msg proto.Message) error {
	sender, err := c.messageSenderForPeer(ctx, p)
	if err != nil {
		log.Debugw("message failed to open message sender", "error", err, "to", p)
		return err
	}

	if err = sender.sendMessage(ctx, msg, c.host, c.protocols); err != nil {
		log.Debugw("message failed", "error", err, "to", p)
		return err
	}

	return nil
}

func (c *Client) peerSender(peerID peer.ID) *peerMessageSender {
	c.sendersLock.Lock()
	defer c.sendersLock.Unlock()

	ms, ok := c.peerSenders[peerID]
	if ok {
		return ms
	}
	ms = &peerMessageSender{
		peerID:  peerID,
		ctxLock: newCtxMutex(),
	}
	c.peerSenders[peerID] = ms
	return ms
}

func (c *Client) messageSenderForPeer(ctx context.Context, peerID peer.ID) (*peerMessageSender, error) {
	ms := c.peerSender(peerID)

	if err := ms.prepOrInvalidate(ctx, c.host, c.protocols); err != nil {
		c.sendersLock.Lock()
		defer c.sendersLock.Unlock()

		if msCur, ok := c.peerSenders[peerID]; ok {
			// Changed. Use the new one, old one is invalid and
			// not in the map so we can just throw it away.
			if ms != msCur {
				return msCur, nil
			}
			// Not changed, remove the now invalid stream from the
			// map.
			delete(c.peerSenders, peerID)
		}
		// Invalid but not in map. Must have been removed by a disconnect.
		return nil, err
	}
	// All ready to go.
	return ms, nil
}
