/*
  Construido como parte da disciplina: Sistemas Distribuidos - PUCRS - Escola Politecnica
  Professor: Fernando Dotti  (https://fldotti.github.io/)
  Modulo representando Perfect Point to Point Links tal como definido em:
    Introduction to Reliable and Secure Distributed Programming
    Christian Cachin, Rachid Gerraoui, Luis Rodrigues
  * Semestre 2018/2 - Primeira versao.  Estudantes:  Andre Antonitsch e Rafael Copstein
  * Semestre 2019/1 - Reaproveita conexões TCP já abertas - Estudantes: Vinicius Sesti e Gabriel Waengertner
  * Semestre 2020/1 - Separa mensagens de qualquer tamanho atee 4 digitos.
  Sender envia tamanho no formato 4 digitos (preenche com 0s a esquerda)
  Receiver recebe 4 digitos, calcula tamanho do buffer a receber,
  e recebe com io.ReadFull o tamanho informado - Dotti
  * Semestre 2022/1 - melhorias eliminando retorno de erro aos canais superiores.
  se conexao fecha nao retorna nada.   melhorias em comentarios.   adicionado modo debug. - Dotti
*/

package PP2PLink

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const maxFrameSize = 4 * 1024 * 1024
const linkQueueCapacity = 4096

type PP2PLink_Req_Message struct {
	To      string
	Message string
}

type PP2PLink_Ind_Message struct {
	From    string
	Message string
}

type PP2PLink struct {
	Ind   chan PP2PLink_Ind_Message
	Req   chan PP2PLink_Req_Message
	Run   bool
	dbg   bool
	Cache map[string]net.Conn

	address string
	epoch   string
	seq     uint64 // acessado somente pela goroutine de envio

	seenMu sync.Mutex
	seen   map[string]uint64 // maior sequência entregue por processo e época
}

type wireMessage struct {
	Kind    string `json:"kind"`
	From    string `json:"from"`
	Epoch   string `json:"epoch"`
	Seq     uint64 `json:"seq"`
	Payload string `json:"payload,omitempty"`
}

func NewPP2PLink(address string, debug bool) *PP2PLink {
	epoch, err := newEpoch()
	if err != nil {
		panic(fmt.Sprintf("cannot create PP2PLink epoch: %v", err))
	}
	p2p := &PP2PLink{
		Req:     make(chan PP2PLink_Req_Message, linkQueueCapacity),
		Ind:     make(chan PP2PLink_Ind_Message, linkQueueCapacity),
		Run:     true,
		dbg:     debug,
		Cache:   make(map[string]net.Conn),
		address: address,
		epoch:   epoch,
		seen:    make(map[string]uint64),
	}
	p2p.Start(address)
	p2p.outDbg("Init PP2PLink")
	return p2p
}

func newEpoch() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func (module *PP2PLink) outDbg(message string) {
	if module.dbg {
		fmt.Println("[PP2PLink] " + message)
	}
}

func (module *PP2PLink) Start(address string) {
	go func() {
		listener, err := net.Listen("tcp4", address)
		if err != nil {
			fmt.Printf("[PP2PLink] listen %s failed: %v\n", address, err)
			return
		}
		for {
			conn, err := listener.Accept()
			if err != nil {
				module.outDbg("accept: " + err.Error())
				continue
			}
			go module.receiveLoop(conn)
		}
	}()

	go func() {
		for message := range module.Req {
			module.Send(message)
		}
	}()
}

func (module *PP2PLink) receiveLoop(conn net.Conn) {
	defer conn.Close()
	for {
		var incoming wireMessage
		if err := readFrame(conn, &incoming); err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				module.outDbg("receive: " + err.Error())
			}
			return
		}
		if incoming.Kind != "data" || incoming.From == "" || incoming.Epoch == "" || incoming.Seq == 0 {
			module.outDbg("invalid data frame")
			return
		}

		key := fmt.Sprintf("%s/%s", incoming.From, incoming.Epoch)
		module.seenMu.Lock()
		if incoming.Seq > module.seen[key] {
			module.Ind <- PP2PLink_Ind_Message{From: incoming.From, Message: incoming.Payload}
			module.seen[key] = incoming.Seq
		}
		module.seenMu.Unlock()

		ack := wireMessage{Kind: "ack", From: module.address, Epoch: incoming.Epoch, Seq: incoming.Seq}
		if err := writeFrame(conn, ack); err != nil {
			module.outDbg("ack send: " + err.Error())
			return
		}
	}
}

func (module *PP2PLink) Send(message PP2PLink_Req_Message) {
	module.seq++
	attempts := 0
	wire := wireMessage{
		Kind:    "data",
		From:    module.address,
		Epoch:   module.epoch,
		Seq:     module.seq,
		Payload: message.Message,
	}

	for {
		attempts++
		conn := module.Cache[message.To]
		if conn == nil {
			var err error
			conn, err = net.DialTimeout("tcp", message.To, time.Second)
			if err != nil {
				module.logRetry(attempts, message.To, err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			module.Cache[message.To] = conn
		}

		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		err := writeFrame(conn, wire)
		if err == nil {
			var ack wireMessage
			if err = readFrame(conn, &ack); err == nil && ack.Kind == "ack" && ack.From == message.To && ack.Epoch == module.epoch && ack.Seq == wire.Seq {
				_ = conn.SetDeadline(time.Time{})
				return
			}
		}
		if err == nil {
			err = fmt.Errorf("unexpected acknowledgement")
		}
		module.logRetry(attempts, message.To, err)
		_ = conn.Close()
		delete(module.Cache, message.To)
		time.Sleep(100 * time.Millisecond)
	}
}

func (module *PP2PLink) logRetry(attempt int, destination string, err error) {
	if module.dbg || attempt == 1 || attempt%20 == 0 {
		fmt.Printf("[PP2PLink] retry #%d to %s: %v\n", attempt, destination, err)
	}
}

func writeFrame(writer io.Writer, message wireMessage) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > maxFrameSize {
		return fmt.Errorf("invalid frame size %d", len(body))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, body)
}

func readFrame(reader io.Reader, target *wireMessage) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxFrameSize {
		return fmt.Errorf("invalid frame size %d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
