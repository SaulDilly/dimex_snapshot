# Como executar

## Pré-requisitos

- Go instalado para executar os processos DiMeX.
- .NET 10 SDK instalado para executar o analyzer em C#.

Execute os comandos a partir da pasta raiz do projeto. Abra três terminais e
inicie um processo em cada terminal. Inicie P1 e P2 primeiro, depois P0:

```powershell
go run useDIMEX-f.go 0 127.0.0.1:5000 127.0.0.1:6001 127.0.0.1:7002
```

```powershell
go run useDIMEX-f.go 1 127.0.0.1:5000 127.0.0.1:6001 127.0.0.1:7002
```

```powershell
go run useDIMEX-f.go 2 127.0.0.1:5000 127.0.0.1:6001 127.0.0.1:7002
```

O processo 0 inicia 300 snapshots. Aguarde a mensagem `[snapshot] 300/300 completos`
no terminal do processo 0. Em seguida, pressione `Ctrl+C` nos três terminais.
Os registros ficam nos arquivos `snap_p0.txt`, `snap_p1.txt` e `snap_p2.txt` na
pasta em que os processos foram iniciados.

## Analisar os snapshots

Depois de encerrar os três processos, execute na raiz do projeto:

```powershell
dotnet run --file ./tools/analyze_snapshots.cs -- -dir . -processes 3 -expected 300
```

O resumo final mostra quantos snapshots foram analisados e quantos problemas
foram encontrados.