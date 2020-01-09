//Package haraqa (High Availability Routing And Queueing Application) defines
// the go client for communicating with the haraqa broker:
// https://hub.docker.com/repository/docker/haraqa/haraqa .
//
package haraqa

import (
	"context"
	"io"
	"net"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/haraqa/haraqa/internal/protocol"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

var (
	//ErrTopicExists is returned if a CreateTopic request is made to an existing topic
	ErrTopicExists = protocol.ErrTopicExists
	//ErrTopicDoesNotExist is returned if a Request is made on a non existent topic
	ErrTopicDoesNotExist = protocol.ErrTopicDoesNotExist
)

//DefaultConfig is the configuration for standard, local deployment of the haraqa broker
var DefaultConfig = Config{
	Host:         "127.0.0.1",
	GRPCPort:     4353,
	DataPort:     14353,
	CreateTopics: true,
	Timeout:      time.Second * 5,
}

//Config for new clients, see DefaultConfig for recommended values
type Config struct {
	Host         string        // address of the haraqa broker
	GRPCPort     int           // broker's grpc port (default 4353)
	DataPort     int           // broker's data port (default 14353)
	CreateTopics bool          // if a topic does not exist, automatically create it
	UnixSocket   string        // if set, the unix socket is used for the data connection
	Timeout      time.Duration // the timeout for grpc requests
}

// Client is the connection to the haraqa broker. While it's technically possible
// to produce and consume using the same client, it's recommended to use separate
// clients for producing and consuming. Use NewClient(config) to start a client
// session.
type Client struct {
	config       Config
	grpcConn     *grpc.ClientConn
	client       protocol.HaraqaClient
	dataConnLock sync.Mutex
	dataConn     net.Conn
	dataBuf      []byte
}

// NewClient creates a new haraqa client based on the given config
//  cfg := haraqa.DefaultConfig
//  client, err := haraqa.NewClient(cfg)
//  if err != nil {
//    panic(err)
//  }
//  defer client.Close()
func NewClient(config Config) (*Client, error) {
	if config.Host == "" {
		return nil, errors.New("invalid host")
	}
	if config.GRPCPort == 0 || (config.UnixSocket == "" && config.DataPort == 0) {
		return nil, errors.New("invalid ports")
	}

	// Set up a connection to the server.
	grpcConn, err := grpc.Dial(config.Host+":"+strconv.Itoa(config.GRPCPort), grpc.WithInsecure(), grpc.WithBlock(), grpc.WithTimeout(config.Timeout))
	if err != nil {
		return nil, errors.Wrapf(err, "unable to connect to grpc port %q", config.Host+":"+strconv.Itoa(config.GRPCPort))
	}
	client := protocol.NewHaraqaClient(grpcConn)
	if client == nil {
		return nil, errors.New("unable to create new grpc client")
	}

	c := &Client{
		config:   config,
		grpcConn: grpcConn,
		client:   client,
	}

	return c, nil
}

// Close closes the client connection
func (c *Client) Close() error {
	c.dataConnLock.Lock()
	defer c.dataConnLock.Unlock()

	var errs []error

	if err := c.grpcConn.Close(); err != nil {
		errs = append(errs, err)
	}
	if c.dataConn != nil {
		if err := c.dataConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Wrapf(errs[0], "error closing haraqa client total errors: (%d)", len(errs))
	}
	return nil
}

// dataConnect connects a new data client connection to the haraqa broker. it should be called
// before any consume or produce grpc calls
func (c *Client) dataConnect() error {
	if c.dataConn != nil {
		return nil
	}
	var err error
	var dataConn net.Conn
	// connect to data port
	if c.config.UnixSocket != "" {
		dataConn, err = net.Dial("unix", c.config.UnixSocket)
		if err != nil {
			return errors.Wrapf(err, "unable to connect to unix socket %q", c.config.UnixSocket)
		}
	} else {
		dataConn, err = net.Dial("tcp", c.config.Host+":"+strconv.Itoa(c.config.DataPort))
		if err != nil {
			return errors.Wrapf(err, "unable to connect to data port %q", c.config.Host+":"+strconv.Itoa(c.config.DataPort))
		}
	}

	c.dataConn = dataConn
	return nil
}

//CreateTopic creates a new topic. It returns a ErrTopicExists error if the
// topic has already been created
func (c *Client) CreateTopic(ctx context.Context, topic []byte) error {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	// send message request
	r, err := c.client.CreateTopic(ctx, &protocol.CreateTopicRequest{
		Topic: topic,
	})
	if err != nil {
		return errors.Wrap(err, "could not produce")
	}
	meta := r.GetMeta()
	if !meta.GetOK() {
		switch meta.GetErrorMsg() {
		case protocol.ErrTopicExists.Error():
			err = protocol.ErrTopicExists
		default:
			err = errors.New(meta.GetErrorMsg())
		}
		return errors.Wrapf(err, "broker error creating topic %q", string(topic))
	}
	return nil
}

// DeleteTopic permanentaly deletes all messages in a topic queue
func (c *Client) DeleteTopic(ctx context.Context, topic []byte) error {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	// send message request
	r, err := c.client.DeleteTopic(ctx, &protocol.DeleteTopicRequest{
		Topic: topic,
	})
	if err != nil {
		return errors.Wrap(err, "could not produce")
	}
	meta := r.GetMeta()
	if !meta.GetOK() {
		return errors.Wrapf(errors.New(meta.GetErrorMsg()), "broker error deleting topic %q", string(topic))
	}
	return nil
}

// ListTopics queries the broker for a list of topics. If regex is given, topics are filtered to match
func (c *Client) ListTopics(ctx context.Context, regex string) ([][]byte, error) {
	// check regex before attempting
	_, err := regexp.Compile(regex)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	// send message request
	r, err := c.client.ListTopics(ctx, &protocol.ListTopicsRequest{Regex: regex})
	if err != nil {
		return nil, errors.Wrap(err, "could not produce")
	}
	meta := r.GetMeta()
	if !meta.GetOK() {
		return nil, errors.Wrap(errors.New(meta.GetErrorMsg()), "broker error listing topics")
	}
	return r.GetTopics(), nil
}

// Produce one or more messages as a batch to a common topic
func (c *Client) Produce(ctx context.Context, topic []byte, msgs ...[]byte) error {
	if len(msgs) == 0 {
		return nil
	}

	// reconnect to data endpoint if required
	err := c.dataConnect()
	if err != nil {
		return err
	}

	return c.produce(ctx, topic, msgs...)
}

func (c *Client) produce(ctx context.Context, topic []byte, msgs ...[]byte) error {
	c.dataConnLock.Lock()
	defer c.dataConnLock.Unlock()

	msgSizes := make([]int64, len(msgs))
	var totalSize int64
	for i := range msgs {
		msgSizes[i] = int64(len(msgs[i]))
		totalSize += msgSizes[i]
	}

	protocol.ExtendBuffer(&c.dataBuf, int(totalSize))
	req := protocol.ProduceRequest{
		Topic:    topic,
		MsgSizes: msgSizes,
	}
	err := req.Write(c.dataConn)
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return errors.Wrap(err, "could not write produce header")
	}

	// send messages
	var n int
	for i := range msgs {
		n += copy(c.dataBuf[n:], msgs[i])
	}
	_, err = c.dataConn.Write(c.dataBuf[:n])
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return errors.Wrap(err, "unable to write data connection")
	}

	var prefix [6]byte
	p, _, err := protocol.ReadPrefix(c.dataConn, prefix[:])
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return errors.Wrap(err, "could not read from data connection")
	}
	if p != protocol.TypeProduce {
		c.dataConn.Close()
		c.dataConn = nil
		return errors.Wrapf(err, "invalid response read from data connection")
	}

	return nil
}

// ProduceMsg is the message structure for sending messages to a ProduceLoop channel.
// The Err channel must be set with a capacity of 1 or greater to receive an error response.
// if the message was produced successfully a nil error is returned.
type ProduceMsg struct {
	Msg []byte
	Err chan error
}

// NewProduceMsg returns a ProduceMsg with a new Err channel. Use with the ProduceLoop method.
func NewProduceMsg(msg []byte) ProduceMsg {
	return ProduceMsg{
		Msg: msg,
		Err: make(chan error, 1),
	}
}

// ProduceLoop starts a loop that reads from the channel and sends the messages as
// a batch to the broker. The batch size, the number of messages in a batch, is equal
// to the capacity of the channel given.
// If the capacity is 0 an error is returned. Batches do not need to be filled before being
// sent so it is recommended to set the batch size to a reasonably high value.
// Messages are sent when either the number of messages reaches the channel capacity
// or when the channel has been drained and there are no remaining messages in the channel.
// If the channel is closed the ProduceLoop is gracefully stopped and any remaining
// messages in the channel are sent
func (c *Client) ProduceLoop(ctx context.Context, topic []byte, ch chan ProduceMsg) error {
	if cap(ch) == 0 {
		return errors.New("invalid channel capacity, channels must have a capacity of at least 1")
	}

	// reconnect to data endpoint if required
	if err := c.dataConnect(); err != nil {
		return err
	}

	errs := make([]chan error, 0, cap(ch))
	msgs := make([][]byte, 0, cap(ch))

	for msg := range ch {
		msgs = append(msgs, msg.Msg)
		if msg.Err != nil {
			errs = append(errs, msg.Err)
		}
		if len(msgs) == cap(ch) || (len(ch) == 0 && len(msgs) > 0) {
			// send produce batch
			err := c.produce(ctx, topic, msgs...)
			for i := range errs {
				errs[i] <- err
			}
			if err != nil {
				return err
			}
			// truncate msg buffer
			msgs = msgs[:0]
			errs = errs[:0]
		}
	}

	if len(msgs) > 0 {
		//send one last batch
		err := c.produce(ctx, topic, msgs...)
		for i := range errs {
			errs[i] <- err
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Offsets returns the min and max offsets available for a topic
//  min, max, err := client.Offset([]byte("myTopic"))
func (c *Client) Offsets(ctx context.Context, topic []byte) (int64, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	resp, err := c.client.Offsets(ctx, &protocol.OffsetRequest{
		Topic: topic,
	})
	if err != nil {
		return 0, 0, err
	}
	if !resp.GetMeta().GetOK() {
		if resp.GetMeta().GetErrorMsg() == protocol.ErrTopicDoesNotExist.Error() {
			return 0, 0, protocol.ErrTopicDoesNotExist
		}
		return 0, 0, errors.New(resp.GetMeta().GetErrorMsg())
	}

	return resp.GetMinOffset(), resp.GetMaxOffset(), nil
}

// ConsumeBuffer is a reusable set of buffers used to consume
type ConsumeBuffer struct {
	headerBuf []byte
	bodyBuf   []byte
	msgSizes  []int64
	msgBuf    [][]byte
}

// NewConsumeBuffer instantiates a new ConsumeBuffer
func NewConsumeBuffer() *ConsumeBuffer {
	return new(ConsumeBuffer)
}

// Consume sends a consume request and returns a batch of messages, buf can be nil
func (c *Client) Consume(ctx context.Context, topic []byte, offset int64, maxBatchSize int64, buf *ConsumeBuffer) ([][]byte, error) {
	if buf == nil {
		buf = NewConsumeBuffer()
	}
	c.dataConnLock.Lock()
	defer c.dataConnLock.Unlock()

	err := c.dataConnect()
	if err != nil {
		return nil, errors.Wrap(err, "could not connect to data port")
	}

	req := protocol.ConsumeRequest{
		Topic:        topic,
		Offset:       offset,
		MaxBatchSize: maxBatchSize,
	}

	err = req.Write(c.dataConn)
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return nil, errors.Wrap(err, "could not write to data connection")
	}

	var prefix [6]byte
	p, hLen, err := protocol.ReadPrefix(c.dataConn, prefix[:])
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return nil, errors.Wrapf(err, "could not read from data connection")
	}
	if p != protocol.TypeConsume {
		c.dataConn.Close()
		c.dataConn = nil
		return nil, errors.Wrapf(err, "invalid response type read from data connection")
	}

	protocol.ExtendBuffer(&buf.headerBuf, int(hLen))
	_, err = io.ReadFull(c.dataConn, buf.headerBuf)
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return nil, errors.Wrap(err, "could not read error from data connection")
	}

	resp := protocol.ConsumeResponse{
		MsgSizes: buf.msgSizes,
	}
	err = resp.Read(buf.headerBuf)
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return nil, errors.Wrapf(err, "invalid response read from data connection")
	}
	if cap(resp.MsgSizes) > cap(buf.msgSizes) {
		buf.msgSizes = resp.MsgSizes
	}

	totalSize := sum(resp.MsgSizes)
	protocol.ExtendBuffer(&buf.bodyBuf, int(totalSize))

	_, err = io.ReadFull(c.dataConn, buf.bodyBuf)
	if err != nil {
		c.dataConn.Close()
		c.dataConn = nil
		return nil, errors.Wrap(err, "unable to read batch messages from connection")
	}

	if len(resp.MsgSizes) > cap(buf.msgBuf) {
		buf.msgBuf = append(buf.msgBuf, make([][]byte, len(resp.MsgSizes)-len(buf.msgBuf))...)
	}
	buf.msgBuf = (buf.msgBuf)[:len(resp.MsgSizes)]

	var n int64
	for i := range resp.MsgSizes {
		buf.msgBuf[i] = buf.bodyBuf[n : n+resp.MsgSizes[i] : n+resp.MsgSizes[i]]
		n += resp.MsgSizes[i]
	}
	return buf.msgBuf, nil
}

func sum(in []int64) int64 {
	var out int64
	for i := range in {
		out += in[i]
	}
	return out
}
