// This is a streaming interface to allow data to be streamed to Spark or
// something similar over a TCP socket.  This worker creates a TCP service
// which clients can connect to.  When JSON events are received, they are
// sent to the socket readers, newline appended.  Clients can come and go as
// they please.  A buffer of up to 1000 messages per client is kept.  If
// clients slow up, messages get discarded.  When no clients are connected,
// messages get discarded.

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io/ioutil"
	"net"
	"os"
	"sync"
	"time"
	"context"
	
	"github.com/trustnetworks/analytics-common/utils"
	"github.com/trustnetworks/analytics-common/worker"
)

const pgm = "streamer"

// Client structure describes a streaming client which has connected to receive
// data
type Client struct {
	// Network connection
	connection *net.Conn

	// Running? Set to false to terminate.
	running bool

	// Queue of messages to send
	queue chan []uint8
}

type work struct {

	// Port number to listen on
	port string

	// private key file
	key []byte

	// Lock for the clients array
	clientLock sync.Mutex

	// Clients array, one element for each connection
	clients []*Client
}

// Handles incoming requests.
func (client *Client) handleRequest(s *work) {

	utils.Log("Connection...")

	conn := client.connection
	defer (*conn).Close()

	// Start with a simple challenge/response to prove the connector knows
	// the key

	// Send a challenge to the client, they prepend the password and return
	// a SHA256 hash of the whole thing.

	// Create 32-bytes of random
	challenge := make([]byte, 32)
	_, err := rand.Read(challenge)
	if err != nil {
		utils.Log("rand.Read failed: %s", err.Error())
		return
	}

	// Convert to base64 and send to the client
	encoded := base64.StdEncoding.EncodeToString(challenge)
	(*conn).Write([]byte(encoded))
	(*conn).Write([]byte("\n"))

	// Create a SHA256 hash of password plus colon plus random challenge.
	h := sha256.New()
	h.Write(s.key)
	h.Write([]byte(":"))
	h.Write(challenge)

	// Convert the hash to base64.
	expected := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Read a single line of text until LF (ignore CR).
	received := []byte{}
	b := make([]byte, 1)
	for {
		_, err = (*conn).Read(b)
		if err != nil {
			utils.Log("Read failed: %s", err.Error())
			return
		}
		if b[0] == '\r' {
			continue
		}
		if b[0] == '\n' {
			break
		}
		received = append(received, []byte(b)...)
	}

	// Compare expected with provided.
	if string(received) != expected {
		utils.Log("Invalid key exchange")
		return
	}

	utils.Log("Authorised connection.")

	// Add client to client list.
	s.clientLock.Lock()
	s.clients = append(s.clients, client)
	s.clientLock.Unlock()

	for client.running {

		// Select statement completes when either a message is received
		// on the queue, or after a short timeout.  This allows
		// client.running to be re-evaluated.
		select {

		// Message on queue.
		case s := <-client.queue:

			// Append newline and send to client
			_, err := (*conn).Write([]byte(string(s) + "\n"))
			if err != nil {

				// On error, close the connection
				client.running = false
				break
			}

			// Timeout exists to allow client.running to be
			// re-evaluated.
		case <-time.After(time.Second):

		}

	}

	// Remove me from the clients list.
	s.clientLock.Lock()
	defer s.clientLock.Unlock()
	for k, _ := range s.clients {

		// Is it me?
		if s.clients[k] == client {

			// Delete from the clients list
			s.clients = append(s.clients[:k], s.clients[k+1:]...)
			return
		}
	}

}

func (s *work) init() error {

	// Get port number, default is 3333.
	s.port = utils.Getenv("PORT", "3333")

	// Get key
	keyfile := utils.Getenv("KEY", "/key/key")

	var err error
	s.key, err = ioutil.ReadFile(keyfile)
	if err != nil {
		utils.Log("Couldn't read key file: %s", err.Error())
		return err
	}

	// Start listener.
	go s.listen()

	return nil

}

func (s *work) listen() {

	// Listen for incoming connections.  FIXME: IPv6?
	l, err := net.Listen("tcp", "0.0.0.0:"+s.port)
	if err != nil {
		utils.Log("Error listening: %s", err.Error())
		os.Exit(1)
	}

	// Close the listener when the application closes.
	defer l.Close()

	utils.Log("Listening on port %s...", s.port)

	for {
		// Listen for an incoming connection.
		conn, err := l.Accept()
		if err != nil {
			utils.Log("Error accepting: %s", err.Error())
			return
		}

		// Create client
		client := &Client{
			connection: &conn,
			running:    true,
			queue:      make(chan []uint8, 1000),
		}

		// Handle connections in a new goroutine.
		go client.handleRequest(s)

	}

}

func (s *work) Handle(msg []uint8, w *worker.Worker) error {

	// Got a message, lock the client list for the duration of this.
	s.clientLock.Lock()
	defer s.clientLock.Unlock()

	// Loop through clients list.
	for _, v := range s.clients {

		// Send a message.  The select statement means that a message
		// will not get queued if the client list is full i.e.
		// slow reader.
		select {
		case v.queue <- msg:
		default:
			// Queue is full, ignore.
		}
	}

	return nil

}

func main() {

	var w worker.QueueWorker
	var s work
	utils.LogPgm = pgm

	utils.Log("Initialising...")

	err := s.init()
	if err != nil {
		utils.Log("init: %s", err.Error())
		return
	}

	var input string
	var output []string

	if len(os.Args) > 0 {
		input = os.Args[1]
	}
	if len(os.Args) > 2 {
		output = os.Args[2:]
	}

	// context to handle control of subroutines
	ctx := context.Background()
	ctx, cancel := utils.ContextWithSigterm(ctx)
	defer cancel()

	err = w.Initialise(ctx, input, output, pgm)
	if err != nil {
		utils.Log("init: %s", err.Error())
		return
	}

	utils.Log("Initialisation complete.")

	// Invoke Wye event handling.
	err = w.Run(ctx, &s)
	if err != nil {
		utils.Log("error: Event handling failed with err: %s", err.Error())
	}

}
