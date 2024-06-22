package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Config struct {
	Address            string `json:"address"`
	EnableTextEndpoint bool   `json:"enableTextEndpoint"`
	Files              struct {
		Text  string `json:"text"`
		Audio string `json:"audio"`
		HTML  string `json:"html"`
	} `json:"files"`
	Voices            map[string][][]string `json:"voices"`
	AllowedCharacters string                `json:"allowedCharacters"`
	TextLengthLimit   int                   `json:"textLengthLimit"`
}

var config Config
var m sync.Mutex
var allowedCharacters = map[rune]bool{}

func HttpHandler(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/":
		switch req.Method {
		case http.MethodGet:
			if config.Files.HTML == "" {
				http.Error(w, "404", http.StatusNotFound)
			} else {
				http.ServeFile(w, req, config.Files.HTML)
			}
		case http.MethodPost:
			bodyBytes, err := io.ReadAll(req.Body)
			if err != nil {
				return
			}

			text := string(bodyBytes)

			if len(text) == 0 || (config.TextLengthLimit > 0 && len(text) > config.TextLengthLimit) {
				http.Error(w, "400", http.StatusBadRequest)
				return
			}

			if len(config.AllowedCharacters) > 0 {
				for _, char := range text {
					if !allowedCharacters[char] {
						http.Error(w, "400", http.StatusBadRequest)
						return
					}
				}
			}

			if voiceCommands, found := config.Voices[req.Header.Get("Voice")]; found {
				m.Lock()

				defer func() {
					os.Remove(config.Files.Text)
					os.Remove(config.Files.Audio)
					m.Unlock()
				}()

				textFile, err := os.Create(config.Files.Text)
				if err != nil {
					fmt.Println(err)
					http.Error(w, "500", http.StatusInternalServerError)
					return
				}

				_, err = textFile.WriteString(text)
				if err != nil {
					fmt.Println(err)
					textFile.Close()
					http.Error(w, "500", http.StatusInternalServerError)
					return
				}

				err = textFile.Close()
				if err != nil {
					fmt.Println(err)
					http.Error(w, "500", http.StatusInternalServerError)
					return
				}

				for _, c := range voiceCommands {
					cmd := exec.Command(c[0], c[1:]...)
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr

					err = cmd.Run()
					if err != nil {
						fmt.Println(err)
						http.Error(w, "500", http.StatusInternalServerError)
						return
					}
				}

				audioFile, err := os.Open(config.Files.Audio)
				if err != nil {
					fmt.Println(err)
					http.Error(w, "500", http.StatusInternalServerError)
					return
				}

				switch filepath.Ext(config.Files.Audio) {
				case ".wav":
					w.Header().Set("Content-Type", "audio/wav")
				case ".flac":
					w.Header().Set("Content-Type", "audio/flac")
				default:
					w.Header().Set("Content-Type", "application/octet-stream")
				}

				io.Copy(w, audioFile)
				audioFile.Close()
			} else {
				http.Error(w, "400", http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case "/text":
		if !config.EnableTextEndpoint {
			http.Error(w, "404", http.StatusNotFound)
			return
		}

		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		textFile, err := os.Open(config.Files.Text)
		if err != nil {
			fmt.Println(err)
			http.Error(w, "500", http.StatusInternalServerError)
			return
		}

		io.Copy(w, textFile)
		textFile.Close()
	case "/voices":
		voices := make([]string, len(config.Voices))
		i := 0
		for k := range config.Voices {
			voices[i] = k
			i++
		}

		sort.Strings(voices)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(voices)
	default:
		http.Error(w, "404", http.StatusNotFound)
	}
}

func main() {
	var configBytes []byte
	var err error

	if len(os.Args) < 2 || os.Args[1] == "-" {
		configBytes, err = io.ReadAll(os.Stdin)
	} else if strings.HasPrefix(os.Args[1], "http://") || strings.HasPrefix(os.Args[1], "https://") {
		response, err := http.Get(os.Args[1])
		if err != nil {
			panic(err)
		}

		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			os.Exit(1)
		}

		configBytes, err = io.ReadAll(response.Body)
		response.Body.Close()
	} else {
		configBytes, err = os.ReadFile(os.Args[1])
	}

	if err != nil {
		panic(err)
	}

	err = json.Unmarshal(configBytes, &config)
	if err != nil {
		panic(err)
	}

	if config.Address == "" || config.Files.Text == "" || config.Files.Audio == "" || len(config.Voices) == 0 {
		os.Exit(1)
	}

	for _, ch := range config.AllowedCharacters {
		allowedCharacters[ch] = true
	}

	http.HandleFunc("/", HttpHandler)
	http.ListenAndServe(config.Address, nil)
}
