package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/0xef53/go-qmp/v2"
	"github.com/0xef53/liner"
    "github.com/robertkrimen/isatty"
)

var (
	Error = log.New(os.Stdout, "qmp_shell error: ", 0)

	ErrBadCommandFormat = errors.New("command format: <command-name>  [arg-name1=arg1] ... [arg-nameN=argN]")
)

type QMPCommand qmp.Command

type QMPShell struct {
	monitor *qmp.Monitor
	line    *liner.State
	vmname  string
	prompt  string
	banner  string
	qemuVer string
	isHMP   bool
}

func NewQMPShell(socket string) (*QMPShell, error) {
	monitor, err := qmp.NewMonitor(socket, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to the socket: %s", socket)
	}

	// Getting the virtual machine name
	vm := struct {
		Name string `json:"name"`
	}{}

	if err := monitor.Run(QMPCommand{"query-name", nil}, &vm); err != nil {
		return nil, err
	}

	// Getting the QEMU version
	version := struct {
		Qemu struct {
			Major int `json:"major"`
			Minor int `json:"minor"`
			Micro int `json:"micro"`
		} `json:"qemu"`
	}{}

	if err := monitor.Run(QMPCommand{"query-version", nil}, &version); err != nil {
		return nil, err
	}

	// Building the QMP command list
	qmpCommands := []struct {
		Name string `json:"name"`
	}{}

	if err := monitor.Run(QMPCommand{"query-commands", nil}, &qmpCommands); err != nil {
		return nil, fmt.Errorf("cannot build the QMP command list: %s", err)
	}

	var cmdlist []string

	for _, cmd := range qmpCommands {
		cmdlist = append(cmdlist, cmd.Name)
	}

	sort.Strings(cmdlist)

	// Configuring the linear
	line := liner.NewLiner()
	line.SetCtrlCAborts(true)

	line.SetCompleter(func(line string) (c []string) {
		for _, n := range cmdlist {
			if strings.HasPrefix(n, strings.ToLower(line)) {
				c = append(c, n)
			}
		}
		return
	})

	line.SetTabCompletionStyle(liner.TabPrints)

	// Building the shell
	shell := QMPShell{
		monitor: monitor,
		line:    line,
		vmname:  vm.Name,
		prompt:  fmt.Sprintf("qmp_shell/%s> ", vm.Name),
		banner:  "Welcome to the QMP low-level shell",
		qemuVer: fmt.Sprintf("%d.%d.%d", version.Qemu.Major, version.Qemu.Minor, version.Qemu.Micro),
	}

	return &shell, nil
}

func (s *QMPShell) Close() {
	defer s.monitor.Close()
	defer s.line.Close()
}

func (s *QMPShell) LoadHistory(histfile string) error {
	f, err := os.Open(histfile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading history file: %s", err)
	}
	defer f.Close()

	s.line.ReadHistory(f)

	return nil
}

func (s *QMPShell) SaveHistory(histfile string) error {
	f, err := os.Create(histfile)
	if err != nil {
		return fmt.Errorf("writing history file: %s", err)
	}
	defer f.Close()

	s.line.WriteHistory(f)

	return nil
}

func (s *QMPShell) Serve() error {
	fmt.Println(s.banner)
	fmt.Println("Connected to QEMU", s.qemuVer)
	fmt.Println()

	var ts uint64

	for {
		cmdline, err := s.line.Prompt(s.prompt)
		switch err {
		case nil:
			if len(cmdline) == 0 {
				if events, found := s.monitor.FindEvents("", ts); found {
					for _, e := range events {
						fmt.Printf(
							"Received QMP Event %s: %v, Timestamp: seconds = %d, microseconds = %d\n",
							e.Type,
							e.Data,
							e.Timestamp.Seconds,
							e.Timestamp.Microseconds,
						)
						ts = e.Timestamp.Seconds + 1
					}
				}
				continue
			}
			s.line.AppendHistory(cmdline)
			if res, err := s.executeCommand(cmdline); err == nil {
				fmt.Println(res)
			} else {
				fmt.Println(err)
			}
		case liner.ErrPromptAborted:
			log.Print("Aborted")
			return nil
		default:
			fmt.Println()
			return nil
		}
	}

	return nil
}

func (s *QMPShell) Execute(cmdline string) (string, error) {
	return s.executeCommand(cmdline)
}

func (s *QMPShell) executeCommand(cmdline string) (string, error) {
	if s.isHMP {
		cmdline = fmt.Sprintf("human-monitor-command command-line='%s'", cmdline)
	}

	cmd, err := s.buildQMPCommand(cmdline)
	if err != nil {
		return "", err
	}

	var res interface{}

	if err := s.monitor.Run(cmd, &res); err != nil {
		return "", err
	}

	if cmd.Name == "human-monitor-command" {
		return fmt.Sprintf("%s", res), nil
	}

	if strB, err := json.MarshalIndent(res, "", "    "); err == nil {
		return string(strB), nil
	} else {
		return "", nil
	}
}

func (s *QMPShell) buildQMPCommand(cmdline string) (*QMPCommand, error) {
	cmdargs := s.splitString(cmdline, ' ')

	if len(cmdargs) == 0 {
		return nil, ErrBadCommandFormat
	}

	m := make(map[string]interface{})

	for _, arg := range cmdargs[1:] {
		parts := s.splitString(arg, '=')

		if len(parts) != 2 || len(parts[1]) == 0 {
			return nil, ErrBadCommandFormat
		}

		parts[1] = strings.Trim(parts[1], "\"'")

		switch {
		case strings.ToLower(parts[1]) == "true":
			m[parts[0]] = true
		case strings.ToLower(parts[1]) == "false":
			m[parts[0]] = false
		case parts[1][0] == '{' || parts[1][0] == '[':
			var value interface{}
			fmt.Println(parts[1])
			if err := json.Unmarshal([]byte(string(parts[1])), &value); err != nil {
				return nil, fmt.Errorf("JSON parsing error: %s", err)
			}
			m[parts[0]] = value
		default:
			if d, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
				m[parts[0]] = d
			} else {
				m[parts[0]] = parts[1]
			}
		}
	}

	return &QMPCommand{cmdargs[0], m}, nil
}

func (s *QMPShell) splitString(str string, sep rune) []string {
	lastQuote := rune(0)
	f := func(c rune) bool {
		switch {
		case c == lastQuote:
			lastQuote = rune(0)
			return false
		case lastQuote != rune(0):
			return false
		case unicode.In(c, unicode.Quotation_Mark):
			lastQuote = c
			return false
		default:
			if sep == ' ' {
				return unicode.IsSpace(c)
			} else {
				return c == sep
			}
		}
	}

	return strings.FieldsFunc(str, f)
}

type HMPShell struct {
	QMPShell
}

func NewHMPShell(socket string) (*HMPShell, error) {
	shell, err := NewQMPShell(socket)
	if err != nil {
		return nil, err
	}

	shell.isHMP = true
	shell.prompt = fmt.Sprintf("hmp_shell/%s> ", shell.vmname)
	shell.banner = "Welcome to the HMP low-level shell"

	cmdlist := []string{}

	if s, err := shell.executeCommand("help"); err != nil {
		return nil, fmt.Errorf("cannot build the QMP command list: %s", err)
	} else {
		for _, line := range strings.Split(s, "\r\n") {
			if !(len(line) > 0 && line[0] != '[' && line[0] != '\t') {
				continue
			}

			// Drop arguments and help text
			name := strings.Fields(line)[0]

			if name == "info" {
				continue
			}

			if strings.Index(line, "|") != -1 {
				// Command in the form 'foobar|f' or 'f|foobar',
				// take the full name
				nn := strings.Split(name, "|")
				if len(nn[0]) == 1 {
					name = nn[1]
				} else {
					name = nn[0]
				}
			}

			cmdlist = append(cmdlist, name, "help "+name)
		}
	}

	if s, err := shell.executeCommand("info"); err != nil {
		return nil, fmt.Errorf("cannot build the QMP command list: %s", err)
	} else {
		for _, line := range strings.Split(s, "\r\n") {
			if !(len(line) > 0 && len(strings.Fields(line)) >= 2) {
				continue
			}
			cmdlist = append(cmdlist, "info "+strings.Fields(line)[1])
		}
	}

	sort.Strings(cmdlist)

	shell.line.SetCompleter(func(line string) (c []string) {
		for _, n := range cmdlist {
			if strings.HasPrefix(n, strings.ToLower(line)) {
				c = append(c, n)
			}
		}
		return
	})

	return &HMPShell{*shell}, nil
}

func printUsage() {
	s := fmt.Sprintf("Usage:\n  %s [-H] <UNIX socket path>\n\n", filepath.Base(os.Args[0]))
	s += "Options:\n"
	s += "  -H    run the HMP shell instead QMP\n"
	fmt.Fprintf(os.Stderr, s)
	os.Exit(2)
}

type Shell interface {
	Serve() error

	Execute(string) (string, error)

	LoadHistory(string) error
	SaveHistory(string) error

	Close()
}

func init() {
	flag.Usage = printUsage
}

func main() {
	var hmpMode bool

	flag.BoolVar(&hmpMode, "H", hmpMode, "")
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
	}

	vmsocket := flag.Arg(0)

	var shell Shell
	var err error

	if hmpMode {
		shell, err = NewHMPShell(vmsocket)
		if err != nil {
			Error.Fatalln(err)
		}
	} else {
		shell, err = NewQMPShell(vmsocket)
		if err != nil {
			Error.Fatalln(err)
		}
	}
	defer shell.Close()

	if !isatty.Check(os.Stdin.Fd()) {
		r := bufio.NewReader(os.Stdin)
		cmdline, err := r.ReadString('\n')
		if err != nil {
			Error.Fatalln("cannot read command from stdin:", err)
		}
		if res, err := shell.Execute(cmdline); err == nil {
			fmt.Println(res)
		} else {
			Error.Fatalln(err)
		}
		os.Exit(0)
	}

	histfile := "/dev/null"
	if homedir, isSet := os.LookupEnv("HOME"); isSet {
		if hmpMode {
			histfile = filepath.Join(homedir, ".hmpshell_history")
		} else {
			histfile = filepath.Join(homedir, ".qmpshell_history")
		}
	}

	// Load history
	if err := shell.LoadHistory(histfile); err != nil {
		Error.Println(err)
	}

	// Main loop
	if err := shell.Serve(); err != nil {
		Error.Println(err)
	}

	// Save history
	if err := shell.SaveHistory(histfile); err != nil {
		Error.Println(err)
	}
}
