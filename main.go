package main

import (
	"bytes"
	"flag"
	"fmt"
	htmlTemplate "html/template"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"text/template"
	"time"

	"gopkg.in/gomail.v2"
	"gopkg.in/yaml.v2"
)

type server struct {
	addr       string
	mux        *http.ServeMux
	reqCache   *cache
	mailConfig *mailConfig
}

func newServer(cfg *Config) (*server, error) {
	c, err := newCache(cfg.RequestTimeLimit, cfg.RequestTimeLimit/10, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create request cache err=%w", err)
	}

	s := &server{
		addr:     cfg.Addr,
		mux:      http.NewServeMux(),
		reqCache: c,
		mailConfig: &mailConfig{
			To:              cfg.Email.From,
			From:            cfg.Email.From,
			SubjectTemplate: cfg.Email.templateSubject,
			MessageTemplate: cfg.Email.templateMessage,
			SMTP: smtpConfig{
				Host: cfg.Email.SMTP.Host,
				Port: cfg.Email.SMTP.Port,
				User: cfg.Email.SMTP.User,
				Pass: cfg.Email.SMTP.Pass,
			},
		},
	}
	return s, nil
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
	Name    string
	Email   string
	Message string
	Check   string
}

func newMailRequest(name, email, message, check string) (*mailReq, error) {
	if name == "" {
		return nil, fmt.Errorf("missing required name")
	}
	if email == "" {
		return nil, fmt.Errorf("missing required email")
	}
	if message == "" {
		return nil, fmt.Errorf("missing required message")
	}
	if check == "" {
		return nil, fmt.Errorf("missing required check")
	}

	err := validateMessage(message, check)
	if err != nil {
		return nil, fmt.Errorf("check failed err=%v", err)
	}

	result := &mailReq{
		Name:    name,
		Email:   email,
		Message: message,
		Check:   check,
	}

	return result, nil
}

func validateMessage(msg, check string) error {
	sfx := msg[len(msg)-len(check):]
	if check != sfx {
		return fmt.Errorf("message suffix=%v does not match the given check=%#v", sfx, check)
	}
	return nil
}

func (r *mailReq) makeMessage(t *htmlTemplate.Template) (string, error) {
	var buf bytes.Buffer
	err := t.Execute(&buf, r)
	if err != nil {
		return "", fmt.Errorf("failed to execute message template err=%w", err)
	}

	return buf.String(), nil
}

func (r *mailReq) makeSubject(t *template.Template) (string, error) {
	var buf bytes.Buffer
	err := t.Execute(&buf, r)
	if err != nil {
		return "", fmt.Errorf("failed to execute subject template err=%w", err)
	}
	return buf.String(), nil
}

func (s *server) handleMailRequest(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		log.Printf("failed to parse form on request err=%v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if s.reqCache.Exists(r.Form.Get("email")) {
		log.Printf("rejecting request")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	} else {
		s.reqCache.Add(r.Form.Get("email"))
	}

	mr, err := newMailRequest(
		r.Form.Get("name"),
		r.Form.Get("email"),
		r.Form.Get("message"),
		r.Form.Get("check"),
	)
	if err != nil {
		log.Printf("invalid mail request err=%v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err = sendMail(s.mailConfig, mr)
	if err != nil {
		log.Printf("failed to send mail err=%v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Printf("handled mail request to %#v", s.mailConfig.To)
}

type mailConfig struct {
	From            string
	To              string
	SubjectTemplate *template.Template
	MessageTemplate *htmlTemplate.Template
	SMTP            smtpConfig
}

type smtpConfig struct {
	Host string
	Port int
	User string
	Pass string
}

func sendMail(mc *mailConfig, mr *mailReq) error {
	subject, err := mr.makeSubject(mc.SubjectTemplate)
	if err != nil {
		return err
	}

	message, err := mr.makeMessage(mc.MessageTemplate)
	if err != nil {
		return err
	}

	m := gomail.NewMessage()
	m.SetHeader("From", mc.From)
	m.SetHeader("To", mc.From)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", message)

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
	Addr             string        `yaml:"addr"`
	Email            ConfigEmail   `yaml:"email"`
	RequestTimeLimit time.Duration `yaml:"request-time-limit"`
}

type ConfigEmail struct {
	From            string     `yaml:"from"`
	SubjectTemplate string     `yaml:"subject-template"`
	MessageTemplate string     `yaml:"message-template"`
	SMTP            ConfigSMTP `yaml:"smtp"`

	templateSubject *template.Template
	templateMessage *htmlTemplate.Template
}

type ConfigSMTP struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`
	Pass string `yaml:"pass"`
}

func readConfig(fp string) (*Config, error) {
	bt, err := os.ReadFile(fp)
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

	if cf.Email.SubjectTemplate == "" {
		return nil, fmt.Errorf("config is missing email.subject-template")
	}

	cf.Email.templateSubject, err = template.New("subject").Parse(cf.Email.SubjectTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse email.subject-template err=%w", err)
	}

	if cf.Email.MessageTemplate == "" {
		return nil, fmt.Errorf("config is missing email.message-template")
	}
	cf.Email.templateMessage, err = htmlTemplate.New("message").Parse(cf.Email.MessageTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse email.message-template err=%w", err)
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

	srv, err := newServer(cfg)
	if err != nil {
		log.Fatalf("failed to create server err=%v", err)
	}

	err = srv.start()
	if err != nil {
		log.Fatalf("server failed err=%v", err)
	}
}

const AppVersion = "0.0.1"

var buildVersion = ""
var buildTime = ""
