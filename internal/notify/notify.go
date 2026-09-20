package notify

import (
	"log"
	"os/exec"
)

type Notifier interface {
	Send(mt MessageType)
	Error(msg string) // for dynamic errors (e.g., pipeline errors)
}

// NewNotifier creates a notifier based on type with resolved messages.
//
// The "overlay" type is not built here: it needs somewhere to publish to, so
// the daemon constructs it with NewOverlay.
func NewNotifier(notifType string, messages map[MessageType]Message) Notifier {
	switch notifType {
	case "desktop":
		return NewDesktop(messages)
	case "log":
		return NewLog(messages)
	default:
		return &Nop{}
	}
}

// Overlay publishes messages to the on-screen indicator instead of to a
// notification daemon. It only hands the message over; the daemon carries it
// on the status stream and the overlay client draws it.
type Overlay struct {
	messages map[MessageType]Message
	publish  func(Message)
}

func NewOverlay(messages map[MessageType]Message, publish func(Message)) *Overlay {
	return &Overlay{messages: messages, publish: publish}
}

func (o *Overlay) Send(mt MessageType) {
	msg, ok := o.messages[mt]
	if !ok {
		return
	}
	o.publish(msg)
}

func (o *Overlay) Error(msg string) {
	o.publish(Message{Body: msg, IsError: true})
}

type Desktop struct {
	messages map[MessageType]Message
}

func NewDesktop(messages map[MessageType]Message) *Desktop {
	return &Desktop{messages: messages}
}

func (d *Desktop) Send(mt MessageType) {
	msg, ok := d.messages[mt]
	if !ok {
		return
	}
	if msg.IsError {
		d.Error(msg.Body)
		return
	}
	d.notify(msg.Title, msg.Body)
}

func (d *Desktop) Error(msg string) {
	cmd := exec.Command("notify-send", "-a", "Hyprvoice", "-u", "critical", "Hyprvoice Error", msg)
	if err := cmd.Run(); err != nil {
		log.Printf("Failed to send error notification: %v", err)
	}
}

func (d *Desktop) notify(title, body string) {
	cmd := exec.Command("notify-send", "-a", "Hyprvoice", title, body)
	if err := cmd.Run(); err != nil {
		log.Printf("Failed to send notification: %v", err)
	}
}

type Log struct {
	messages map[MessageType]Message
}

func NewLog(messages map[MessageType]Message) *Log {
	return &Log{messages: messages}
}

func (l *Log) Send(mt MessageType) {
	msg, ok := l.messages[mt]
	if !ok {
		return
	}
	if msg.IsError {
		l.Error(msg.Body)
		return
	}
	log.Printf("%s: %s", msg.Title, msg.Body)
}

func (l *Log) Error(msg string) {
	log.Printf("Hyprvoice Error: %s", msg)
}

type Nop struct{}

func (Nop) Send(mt MessageType) {}
func (Nop) Error(msg string)    {}
