// Analisa os registros JSONL produzidos pelo módulo DIMEX.
// Uso: go run ./tools/analyze_snapshots.go -dir . -processes 3
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type snapshot struct {
	SnapID   int        `json:"snapId"`
	PID      int        `json:"pid"`
	St       int        `json:"st"`
	Waiting  []bool     `json:"waiting"`
	Lcl      int        `json:"lcl"`
	ReqTS    int        `json:"reqTs"`
	NbrResps int        `json:"nbrResps"`
	Channels [][]string `json:"canais"`
}

func main() {
	dir := flag.String("dir", ".", "diretório com snap_p<ID>.txt")
	processes := flag.Int("processes", 3, "número de processos do sistema")
	expected := flag.Int("expected", 300, "quantidade de IDs de snapshot esperada, começando em 1")
	flag.Parse()
	if *processes < 1 || *expected < 0 {
		fmt.Fprintln(os.Stderr, "-processes deve ser pelo menos 1 e -expected não pode ser negativo")
		os.Exit(2)
	}

	groups := make(map[int]map[int]snapshot)
	for pid := 0; pid < *processes; pid++ {
		path := filepath.Join(*dir, fmt.Sprintf("snap_p%d.txt", pid))
		file, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "não foi possível abrir %s: %v\n", path, err)
			os.Exit(2)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 4*1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			var s snapshot
			if err := json.Unmarshal(scanner.Bytes(), &s); err != nil {
				fmt.Fprintf(os.Stderr, "%s:%d: JSON inválido: %v\n", path, line, err)
				os.Exit(2)
			}
			if s.PID != pid {
				fmt.Fprintf(os.Stderr, "%s:%d: pid registrado %d, esperado %d\n", path, line, s.PID, pid)
				os.Exit(2)
			}
			if groups[s.SnapID] == nil {
				groups[s.SnapID] = make(map[int]snapshot)
			}
			if _, duplicate := groups[s.SnapID][pid]; duplicate {
				fmt.Fprintf(os.Stderr, "snapshot %d tem registro duplicado para P%d\n", s.SnapID, pid)
				os.Exit(2)
			}
			groups[s.SnapID][pid] = s
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "erro lendo %s: %v\n", path, err)
			os.Exit(2)
		}
		_ = file.Close()
	}

	ids := make([]int, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	violations := 0
	for id := 1; id <= *expected; id++ {
		if _, exists := groups[id]; !exists {
			fmt.Printf("snapshot %d: ausente/incompleto (nenhum registro final em todos os processos)\n", id)
			violations++
		}
	}
	if len(ids) == 0 {
		fmt.Println("nenhum snapshot completo encontrado")
	}
	for _, id := range ids {
		states := groups[id]
		if len(states) != *processes {
			fmt.Printf("snapshot %d: incompleto (%d/%d processos)\n", id, len(states), *processes)
			violations++
			continue
		}
		var issues []string
		inMXCount := 0
		allNoMX := true
		for pid := 0; pid < *processes; pid++ {
			s := states[pid]
			if s.St < 0 || s.St > 2 || len(s.Waiting) != *processes || len(s.Channels) != *processes {
				issues = append(issues, fmt.Sprintf("P%d: estrutura/estado inválido", pid))
				continue
			}
			if s.St == 2 {
				inMXCount++
			}
			if s.St != 0 {
				allNoMX = false
			}
		}
		if inMXCount > 1 {
			issues = append(issues, fmt.Sprintf("Inv1 mutex violado: %d processos em inMX", inMXCount))
		}

		for owner := 0; owner < *processes; owner++ {
			s := states[owner]
			if len(s.Waiting) != *processes {
				continue
			}
			for requester, waiting := range s.Waiting {
				if waiting && s.St == 0 {
					issues = append(issues, fmt.Sprintf("Inv3: P%d guarda resposta para P%d estando noMX", owner, requester))
				}
			}
		}

		if allNoMX {
			for pid := 0; pid < *processes; pid++ {
				s := states[pid]
				for peer, waiting := range s.Waiting {
					if waiting {
						issues = append(issues, fmt.Sprintf("Inv2: todos noMX, mas P%d.waiting[%d]=true", pid, peer))
					}
				}
				for from, messages := range s.Channels {
					if len(messages) > 0 {
						issues = append(issues, fmt.Sprintf("Inv2: todos noMX, mas canal P%d->P%d contém mensagens", from, pid))
					}
				}
			}
		}

		// Para um processo em wantMX, cada peer deve estar representado por
		// resposta já recebida, pedido em trânsito, resposta adiada ou resposta em trânsito.
		for requester := 0; requester < *processes; requester++ {
			r := states[requester]
			if r.St != 1 || len(r.Waiting) != *processes || len(r.Channels) != *processes {
				continue
			}
			accounted := r.NbrResps
			for peer := 0; peer < *processes; peer++ {
				if peer == requester {
					continue
				}
				q := states[peer]
				if len(q.Waiting) == *processes && q.Waiting[requester] {
					accounted++
				}
				if len(q.Channels) == *processes {
					accounted += countMessage(q.Channels[requester], "reqEntry", requester)
				}
				accounted += countMessage(r.Channels[peer], "respOK", peer)
			}
			if accounted != *processes-1 {
				issues = append(issues, fmt.Sprintf("Inv4: P%d quer entrar; respostas + pedidos/respostas em trânsito + waiting = %d, esperado %d", requester, accounted, *processes-1))
			}
		}

		if len(issues) == 0 {
			fmt.Printf("snapshot %d: OK\n", id)
		} else {
			violations += len(issues)
			fmt.Printf("snapshot %d: %d problema(s)\n", id, len(issues))
			for _, issue := range issues {
				fmt.Printf("  - %s\n", issue)
			}
		}
	}
	fmt.Printf("Resumo: %d snapshots, %d problema(s) reportado(s)\n", len(ids), violations)
	if violations > 0 {
		os.Exit(1)
	}
}

func countMessage(messages []string, kind string, sender int) int {
	count := 0
	for _, message := range messages {
		parts := strings.Split(message, "|")
		if len(parts) < 3 || parts[1] != kind {
			continue
		}
		id, err := strconv.Atoi(parts[2])
		if err == nil && id == sender {
			count++
		}
	}
	return count
}
