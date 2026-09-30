# Como executar

## Pré-requisitos

- Go instalado para executar os processos DiMeX.
- .NET 10 SDK instalado para executar o analyzer em C#.

Execute os comandos a partir da raiz do projeto. Antes de cada execução, limpe
o arquivo compartilhado anterior:

```powershell
Set-Content -LiteralPath .\mxOUT.txt -Value "" -NoNewline
```

Abra três terminais. Inicie P1 e P2 primeiro, depois P0; use um terminal por
processo:

```powershell
go run useDIMEX-f.go 1 127.0.0.1:5000 127.0.0.1:6001 127.0.0.1:7002
```

```powershell
go run useDIMEX-f.go 2 127.0.0.1:5000 127.0.0.1:6001 127.0.0.1:7002
```

```powershell
go run useDIMEX-f.go 0 127.0.0.1:5000 127.0.0.1:6001 127.0.0.1:7002
```

Quando P0 imprimir `[snapshot] 300/300 completos`, pressione `Ctrl+C` nos três
terminais. Os registros serão gravados em `snap_p0.txt`, `snap_p1.txt` e
`snap_p2.txt`.

## Verificar a execução normal

O arquivo `mxOUT.txt` deve conter pares `|.` repetidos. Confira-o no PowerShell:

```powershell
$conteudo = Get-Content -LiteralPath .\mxOUT.txt -Raw
if ($conteudo.Length -gt 0 -and [regex]::IsMatch($conteudo, '^(?:\|\.)+$')) {
    "Arquivo no padrão esperado"
} elseif ([regex]::IsMatch($conteudo, '^(?:\|\.)*\|$')) {
    "Sem intercalação; o último acesso parou entre as duas escritas"
} else {
    "Arquivo fora do padrão esperado"
}
```

Depois de encerrar os processos, execute o analyzer:

```powershell
dotnet run --file ./tools/analyze_snapshots.cs -- -dir . -processes 3 -expected 300
```

## Executar os modos de falha

Defina a variável no terminal de cada processo antes de iniciá-lo. Use os
mesmos comandos dos três processos acima:

```powershell
$env:DIMEX_FAULT = 'grant-all'
```

Para testar a ausência de respostas:

```powershell
$env:DIMEX_FAULT = 'never-reply'
```

Após cada teste, encerre os processos e execute o analyzer. Remova a variável
em cada terminal antes de uma execução normal:

```powershell
Remove-Item Env:DIMEX_FAULT -ErrorAction SilentlyContinue
```
