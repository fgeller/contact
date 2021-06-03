package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"gopkg.in/gomail.v2"
	"gopkg.in/yaml.v2"
)

type server struct {
	addr       string
	mux        *http.ServeMux
	mailConfig *mailConfig
}

func newServer(cfg *Config) *server {
	return &server{
		addr: cfg.Addr,
		mux:  http.NewServeMux(),
		mailConfig: &mailConfig{
			To:      cfg.Email.From,
			From:    cfg.Email.From,
			Subject: cfg.Email.Subject,
			SMTP: smtpConfig{
				Host: cfg.Email.SMTP.Host,
				Port: cfg.Email.SMTP.Port,
				User: cfg.Email.SMTP.User,
				Pass: cfg.Email.SMTP.Pass,
			},
		},
	}
}

func (s *server) setupRouting() {
	s.mux.HandleFunc("/mail", s.handleMailRequest)
}

func (s *server) start() error {
	srv := http.Server{
		Addr:           s.addr,
		Handler:        s.mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	s.setupRouting()
	log.Printf("starting server addr=%v", s.addr)
	return srv.ListenAndServe()
}

type mailReq struct {
	name    string
	email   string
	message string
}

func (r *mailReq) makeEmailBody() string {
	return fmt.Sprintf(`Name: %v
Email: %v
Message:
%v
`, r.name, r.email, r.message)
}

func (s *server) handleMailRequest(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		log.Printf("failed to parse form on request err=%v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	mr := &mailReq{
		name:    r.Form.Get("name"),
		email:   r.Form.Get("email"),
		message: r.Form.Get("message"),
	}
	sendMail(s.mailConfig, mr)
	log.Printf("handled mail request to %#v", s.mailConfig.To)
}

type mailConfig struct {
	From    string
	To      string
	Subject string
	SMTP    smtpConfig
}

type smtpConfig struct {
	Host string
	Port int
	User string
	Pass string
}

func sendMail(mc *mailConfig, mr *mailReq) error {
	m := gomail.NewMessage()
	m.SetHeader("From", mc.From)
	m.SetHeader("To", mc.From)
	m.SetHeader("Subject", mc.Subject)
	m.SetBody("text/html", mr.makeEmailBody())

	d := gomail.NewDialer(mc.SMTP.Host, mc.SMTP.Port, mc.SMTP.User, mc.SMTP.Pass)
	return d.DialAndSend(m)
}

type ContactFlags struct {
	Config  string
	Version bool
}

func readFlags() (*ContactFlags, error) {
	var err error
	flg := &ContactFlags{}

	flags := flag.NewFlagSet("contact", flag.ExitOnError)
	flags.StringVar(&flg.Config, "config", "", "Path to config file (default $XDG_CONFIG_HOME/contact/config.yml)")
	flags.BoolVar(&flg.Version, "version", false, "Print version information")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage of contact:\n\n")
		flags.PrintDefaults()
		help := `
Contact serves as a middle man between your website's contact form and your email account.
It serves POST requests and sends the details via email.
`
		fmt.Fprintf(flags.Output(), help)
	}

	err = flags.Parse(os.Args[1:])
	if err != nil {
		return nil, err
	}

	if flg.Version {
		return flg, nil
	}

	if flg.Config == "" {
		df, err := defaultConfigPath()
		if err != nil {
			return nil, fmt.Errorf("failed to check default config file err=%w", err)
		}

		if !fileExists(df) {
			return nil, fmt.Errorf("config is required.")
		}
		flg.Config = df
		log.Printf("found default config file: %#v", df)
	}

	return flg, nil
}

func defaultConfigPath() (string, error) {
	ch := os.Getenv("XDG_CONFIG_HOME")
	if ch == "" {
		u, err := user.Current()
		if err != nil {
			return ch, fmt.Errorf("failed to retrieve current user err=%w", err)
		}
		ch = filepath.Join(u.HomeDir, ".config")
	}
	cp := filepath.Join(ch, "contact", "config.yml")
	return cp, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

type Config struct {
	Addr  string      `yaml:"addr"`
	Email ConfigEmail `yaml:"email"`
}

type ConfigEmail struct {
	From    string     `yaml:"from"`
	Subject string     `yaml:"subject"`
	SMTP    ConfigSMTP `yaml:"smtp"`
}

type ConfigSMTP struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`
	Pass string `yaml:"pass"`
}

func readConfig(fp string) (*Config, error) {
	bt, err := ioutil.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cf Config
	err = yaml.Unmarshal(bt, &cf)

	if err != nil {
		return nil, fmt.Errorf("failed to parse as yaml err=%w", err)
	}

	if cf.Addr == "" {
		return nil, fmt.Errorf("config is missing addr")
	}

	if cf.Email.From == "" {
		return nil, fmt.Errorf("config is missing email.from")
	}

	if cf.Email.Subject == "" {
		return nil, fmt.Errorf("config is missing email.subject")
	}

	if cf.Email.SMTP.Host == "" {
		return nil, fmt.Errorf("config is missing email.smtp.host")
	}

	if cf.Email.SMTP.Port == 0 {
		return nil, fmt.Errorf("config is missing email.smtp.port")
	}

	if cf.Email.SMTP.User == "" {
		return nil, fmt.Errorf("config is missing email.smtp.user")
	}

	if cf.Email.SMTP.Pass == "" {
		return nil, fmt.Errorf("config is missing email.smtp.pass")
	}

	return &cf, err
}

func printVersion() {
	v := fmt.Sprintf("contact version %s", AppVersion)

	if buildVersion != "" || buildTime != "" {
		v += " ("
		if buildVersion != "" {
			v += buildVersion
		}

		if buildTime != "" {
			if buildVersion != "" {
				v += " @ "
			}
			v += buildTime
		}
		v += ")"
	}
	fmt.Println(v)
}

func main() {
	fs, err := readFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse flags err=%v\n", err)
		os.Exit(2)
	}

	if fs.Version {
		printVersion()
		return
	}

	cfg, err := readConfig(fs.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse config err=%v\n", err)
	}

	err = newServer(cfg).start()
	if err != nil {
		log.Fatalf("server failed err=%v", err)
	}
}

const AppVersion = "0.0.1"

var buildVersion = ""
var buildTime = ""
