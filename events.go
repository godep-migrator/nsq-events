package main

import (
  "flag"
  "fmt"
  "log"
  "math/rand"
  "strings"
  "os"
  "os/signal"
  "os/exec"
  "syscall"
  "path/filepath"
  "time"

  "github.com/bitly/go-nsq"
  "github.com/bitly/nsq/util"
)

var (
  showVersion = flag.Bool("version", false, "print version string")

  topic         = flag.String("topic", "", "nsq topic")
  handlersDir  = flag.String("handlers-dir", "", "directory with event handlers")
  channel       = flag.String("channel", "", "nsq channel")
  maxInFlight   = flag.Int("max-in-flight", 200, "max number of messages to allow in flight")
  totalMessages = flag.Int("n", 0, "total messages to show (will wait if starved)")

  readerOpts       = util.StringArray{}
  nsqdTCPAddrs     = util.StringArray{}
  lookupdHTTPAddrs = util.StringArray{}
)

func init() {
  flag.Var(&readerOpts, "reader-opt", "option to passthrough to nsq.Reader (may be given multiple times)")
  flag.Var(&nsqdTCPAddrs, "nsqd-tcp-address", "nsqd TCP address (may be given multiple times)")
  flag.Var(&lookupdHTTPAddrs, "lookupd-http-address", "lookupd HTTP address (may be given multiple times)")
}

type EventRouter struct {
  totalMessages int
  messagesShown int
  handlersDir string
}

func (th *EventRouter) HandleMessage(m *nsq.Message) error {
  th.messagesShown++
  
  msgParts := strings.Split(string(m.Body)," ")
  handlerPath := filepath.Join(th.handlersDir,msgParts[0])
  handlerArguments := strings.Join(msgParts[1:]," ")

  if _, err := os.Stat(handlerPath); os.IsNotExist(err) {
    log.Printf("ignoring event. No handler for event: %s", msgParts[0])
    return nil
  }

  cmd := exec.Command(handlerPath, handlerArguments)
  _, err := cmd.Output()

  if err != nil {
      println(err.Error())
      return nil
  }

  // track user imposed limit (-n flag)

  if th.totalMessages > 0 && th.messagesShown >= th.totalMessages {
    os.Exit(0)
  }
  return nil
}

func main() {
  flag.Parse()

  if *showVersion {
    fmt.Printf("nsq_tail v%s\n", util.BINARY_VERSION)
    return
  }

  if *channel == "" {
    rand.Seed(time.Now().UnixNano())
    *channel = fmt.Sprintf("tail%06d#ephemeral", rand.Int()%999999)
  }

  if *topic == "" {
    log.Fatalf("--topic is required")
  }

  if *handlersDir == "" {
    log.Fatalf("--handlers-dir is required")
  }

  if len(nsqdTCPAddrs) == 0 && len(lookupdHTTPAddrs) == 0 {
    log.Fatalf("--nsqd-tcp-address or --lookupd-http-address required")
  }
  if len(nsqdTCPAddrs) > 0 && len(lookupdHTTPAddrs) > 0 {
    log.Fatalf("use --nsqd-tcp-address or --lookupd-http-address not both")
  }

  sigChan := make(chan os.Signal, 1)
  signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

  r, err := nsq.NewReader(*topic, *channel)
  if err != nil {
    log.Fatalf(err.Error())
  }
  err = util.ParseReaderOpts(r, readerOpts)
  if err != nil {
    log.Fatalf(err.Error())
  }

  // Don't ask for more messages than we want
  if *totalMessages > 0 && *totalMessages < *maxInFlight {
    *maxInFlight = *totalMessages
  }
  r.SetMaxInFlight(*maxInFlight)
  r.AddHandler(&EventRouter{totalMessages: *totalMessages, handlersDir: *handlersDir})

  for _, addrString := range nsqdTCPAddrs {
    err := r.ConnectToNSQ(addrString)
    if err != nil {
      log.Fatalf(err.Error())
    }
  }

  for _, addrString := range lookupdHTTPAddrs {
    log.Printf("lookupd addr %s", addrString)
    err := r.ConnectToLookupd(addrString)
    if err != nil {
      log.Fatalf(err.Error())
    }
  }

  for {
    select {
    case <-r.ExitChan:
      return
    case <-sigChan:
      r.Stop()
    }
  }
}

