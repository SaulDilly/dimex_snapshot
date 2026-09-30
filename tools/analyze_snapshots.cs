#:property PublishAot=false
#:property PublishTrimmed=false

// Analisa os registros JSONL produzidos pelo módulo DIMEX.
// Uso: dotnet run --file ./tools/analyze_snapshots.cs -- -dir . -processes 3

using System.Globalization;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

class Snapshot
{
    // Estes campos refletem o estado salvo por um processo no snapshot.
    [JsonPropertyName("snapId")]
    public int SnapId { get; set; }

    [JsonPropertyName("pid")]
    public int Pid { get; set; }

    [JsonPropertyName("st")]
    public int St { get; set; }

    [JsonPropertyName("waiting")]
    public bool[]? Waiting { get; set; }

    [JsonPropertyName("lcl")]
    public int Lcl { get; set; }

    [JsonPropertyName("reqTs")]
    public int ReqTs { get; set; }

    [JsonPropertyName("nbrResps")]
    public int NbrResps { get; set; }

    [JsonPropertyName("canais")]
    public string?[]?[]? Channels { get; set; }
}

class Options
{
    public string Dir { get; set; } = ".";
    public int Processes { get; set; } = 3;
    public int Expected { get; set; } = 300;
}

static class AnalyzeSnapshots
{
    private const string Usage = "Uso: dotnet run --file ./tools/analyze_snapshots.cs -- [-dir diretório] [-processes número] [-expected número]";

    private static int Main(string[] args)
    {
        if (!TryParseFlags(args, out Options options, out int flagExitCode))
        {
            return flagExitCode;
        }

        // Interrompe a análise se os parâmetros não fizerem sentido.
        if (options.Processes < 1 || options.Expected < 0)
        {
            Console.Error.WriteLine("-processes deve ser pelo menos 1 e -expected não pode ser negativo");
            return 2;
        }

        string dataDir = ResolveDataDirectory(options.Dir, options.Processes);

        // Agrupa os registros por ID do snapshot e por ID do processo.
        var groups = new Dictionary<int, Dictionary<int, Snapshot>>();
        for (int pid = 0; pid < options.Processes; pid++)
        {
            // Cada processo grava seus registros em um arquivo próprio.
            string path = Path.Combine(dataDir, $"snap_p{pid}.txt");
            StreamReader file;
            try
            {
                file = File.OpenText(path);
            }
            catch (Exception ex) when (ex is IOException || ex is UnauthorizedAccessException)
            {
                Console.Error.WriteLine($"não foi possível abrir {path}: {ex.Message}");
                return 2;
            }

            using (file)
            {
                int line = 0;
                while (file.ReadLine() is string jsonLine)
                {
                    line++;
                    // Converte cada linha JSON em um registro de snapshot.
                    Snapshot s;
                    try
                    {
                        if (Encoding.UTF8.GetByteCount(jsonLine) > 4 * 1024 * 1024)
                        {
                            throw new InvalidDataException("bufio.Scanner: token too long");
                        }
                        s = JsonSerializer.Deserialize<Snapshot>(jsonLine) ?? new Snapshot();
                    }
                    catch (Exception ex) when (ex is JsonException || ex is InvalidDataException)
                    {
                        Console.Error.WriteLine($"{path}:{line}: JSON inválido: {ex.Message}");
                        return 2;
                    }

                    if (s.Pid != pid)
                    {
                        Console.Error.WriteLine($"{path}:{line}: pid registrado {s.Pid}, esperado {pid}");
                        return 2;
                    }

                    if (!groups.TryGetValue(s.SnapId, out Dictionary<int, Snapshot>? processStates))
                    {
                        processStates = new Dictionary<int, Snapshot>();
                        groups[s.SnapId] = processStates;
                    }

                    // Um processo deve aparecer no máximo uma vez por snapshot.
                    if (processStates.ContainsKey(pid))
                    {
                        Console.Error.WriteLine($"snapshot {s.SnapId} tem registro duplicado para P{pid}");
                        return 2;
                    }
                    processStates[pid] = s;
                }
            }
        }

        // Ordena os IDs para mostrar os resultados em sequência.
        List<int> ids = groups.Keys.OrderBy(id => id).ToList();
        int violations = 0;
        // Confere se todos os IDs esperados aparecem nos arquivos.
        for (int id = 1; id <= options.Expected; id++)
        {
            if (!groups.ContainsKey(id))
            {
                Console.WriteLine($"snapshot {id}: ausente/incompleto (nenhum registro final em todos os processos)");
                violations++;
            }
        }

        if (ids.Count == 0)
        {
            Console.WriteLine("nenhum snapshot completo encontrado");
        }

        foreach (int id in ids)
        {
            Dictionary<int, Snapshot> states = groups[id];
            // Só é possível verificar invariantes quando todos os processos registraram o snapshot.
            if (states.Count != options.Processes)
            {
                Console.WriteLine($"snapshot {id}: incompleto ({states.Count}/{options.Processes} processos)");
                violations++;
                continue;
            }

            var issues = new List<string>();
            int inMXCount = 0;
            bool allNoMX = true;
            // Valida os estados e conta quantos processos estão na seção crítica.
            for (int pid = 0; pid < options.Processes; pid++)
            {
                Snapshot s = states[pid];
                if (s.St < 0 || s.St > 2 || s.Waiting?.Length != options.Processes || s.Channels?.Length != options.Processes)
                {
                    issues.Add($"P{pid}: estrutura/estado inválido");
                    continue;
                }
                if (s.St == 2)
                {
                    inMXCount++;
                }
                if (s.St != 0)
                {
                    allNoMX = false;
                }
            }

            issues.AddRange(VerificaInvariante1(inMXCount));
            issues.AddRange(VerificaInvariante3(states, options.Processes));
            issues.AddRange(VerificaInvariante2(states, options.Processes, allNoMX));
            issues.AddRange(VerificaInvariante4(states, options.Processes));

            if (issues.Count == 0)
            {
                Console.WriteLine($"snapshot {id}: OK");
            }
            else
            {
                violations += issues.Count;
                Console.WriteLine($"snapshot {id}: {issues.Count} problema(s)");
                foreach (string issue in issues)
                {
                    Console.WriteLine($"  - {issue}");
                }
            }
        }

        // Retorna erro ao terminal quando alguma violação foi encontrada.
        Console.WriteLine($"Resumo: {ids.Count} snapshots, {violations} problema(s) reportado(s)");
        return violations > 0 ? 1 : 0;
    }

    // Inv1: no máximo um processo pode estar na seção crítica.
    private static List<string> VerificaInvariante1(int inMXCount)
    {
        var issues = new List<string>();
        if (inMXCount > 1)
        {
            issues.Add($"Inv1 mutex violado: {inMXCount} processos em inMX");
        }
        return issues;
    }

    // Inv3: um processo fora da seção crítica não deve guardar respostas adiadas.
    private static List<string> VerificaInvariante3(Dictionary<int, Snapshot> states, int processes)
    {
        var issues = new List<string>();
        for (int owner = 0; owner < processes; owner++)
        {
            Snapshot s = states[owner];
            if (s.Waiting?.Length != processes)
            {
                continue;
            }
            for (int requester = 0; requester < s.Waiting.Length; requester++)
            {
                if (s.Waiting[requester] && s.St == 0)
                {
                    issues.Add($"Inv3: P{owner} guarda resposta para P{requester} estando noMX");
                }
            }
        }
        return issues;
    }

    // Inv2: se ninguém está na seção crítica, não pode haver espera ou mensagem pendente.
    private static List<string> VerificaInvariante2(Dictionary<int, Snapshot> states, int processes, bool allNoMX)
    {
        var issues = new List<string>();
        if (allNoMX)
        {
            for (int pid = 0; pid < processes; pid++)
            {
                Snapshot s = states[pid];
                bool[] waiting = s.Waiting ?? Array.Empty<bool>();
                for (int peer = 0; peer < waiting.Length; peer++)
                {
                    if (waiting[peer])
                    {
                        issues.Add($"Inv2: todos noMX, mas P{pid}.waiting[{peer}]=true");
                    }
                }
                string?[]?[] channels = s.Channels ?? Array.Empty<string?[]>();
                for (int from = 0; from < channels.Length; from++)
                {
                    if (channels[from]?.Length > 0)
                    {
                        issues.Add($"Inv2: todos noMX, mas canal P{from}->P{pid} contém mensagens");
                    }
                }
            }
        }
        return issues;
    }

    // Inv4: contabiliza como cada processo responde a quem ainda quer entrar.
    // A resposta pode já ter chegado, estar adiada ou estar em trânsito; o pedido também pode estar em trânsito.
    private static List<string> VerificaInvariante4(Dictionary<int, Snapshot> states, int processes)
    {
        var issues = new List<string>();
        for (int requester = 0; requester < processes; requester++)
        {
            Snapshot r = states[requester];
            if (r.St != 1 || r.Waiting?.Length != processes || r.Channels?.Length != processes)
            {
                continue;
            }

            int accounted = r.NbrResps;
            for (int peer = 0; peer < processes; peer++)
            {
                if (peer == requester)
                {
                    continue;
                }

                Snapshot q = states[peer];
                if (q.Waiting?.Length == processes && q.Waiting[requester])
                {
                    accounted++;
                }
                if (q.Channels?.Length == processes)
                {
                    accounted += CountMessage(q.Channels[requester], "reqEntry", requester);
                }
                accounted += CountMessage(r.Channels[peer], "respOK", peer);
            }

            if (accounted != processes - 1)
            {
                issues.Add($"Inv4: P{requester} quer entrar; respostas + pedidos/respostas em trânsito + waiting = {accounted}, esperado {processes - 1}");
            }
        }
        return issues;
    }

    private static int CountMessage(string?[]? messages, string kind, int sender)
    {
        // Conta mensagens de um tipo e remetente específicos no canal registrado.
        int count = 0;
        foreach (string? message in messages ?? Array.Empty<string?>())
        {
            string[] parts = (message ?? string.Empty).Split('|');
            if (parts.Length < 3 || parts[1] != kind)
            {
                continue;
            }
            if (int.TryParse(parts[2], NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out int id) && id == sender)
            {
                count++;
            }
        }
        return count;
    }

    private static string ResolveDataDirectory(string requestedDir, int processes)
    {
        if (requestedDir != ".")
        {
            return requestedDir;
        }

        bool filesHere = Enumerable.Range(0, processes)
            .All(pid => File.Exists(Path.Combine(requestedDir, $"snap_p{pid}.txt")));
        bool filesInParent = Enumerable.Range(0, processes)
            .All(pid => File.Exists(Path.Combine("..", $"snap_p{pid}.txt")));

        // Se os arquivos não estão na pasta atual, procura na raiz acima de tools.
        return !filesHere && filesInParent ? ".." : requestedDir;
    }

    private static bool TryParseFlags(string[] args, out Options options, out int exitCode)
    {
        options = new Options();
        exitCode = 2;

        for (int i = 0; i < args.Length; i++)
        {
            string arg = args[i];
            if (arg == "--")
            {
                break;
            }
            if (!arg.StartsWith('-'))
            {
                break;
            }

            string flag = arg.TrimStart('-');
            if (flag is "h" or "help")
            {
                Console.WriteLine(Usage);
                Console.WriteLine("  -dir string\n        diretório com snap_p<ID>.txt (padrão .)");
                Console.WriteLine("  -expected int\n        quantidade de IDs de snapshot esperada, começando em 1 (padrão 300)");
                Console.WriteLine("  -processes int\n        número de processos do sistema (padrão 3)");
                exitCode = 0;
                return false;
            }

            string name;
            string value;
            int equalsIndex = flag.IndexOf('=');
            if (equalsIndex >= 0)
            {
                name = flag[..equalsIndex];
                value = flag[(equalsIndex + 1)..];
            }
            else
            {
                name = flag;
                if (i + 1 >= args.Length)
                {
                    Console.Error.WriteLine($"flag needs an argument: -{name}");
                    Console.Error.WriteLine(Usage);
                    return false;
                }
                value = args[++i];
            }

            switch (name)
            {
                case "dir":
                    options.Dir = value;
                    break;
                case "processes":
                case "expected":
                    if (!int.TryParse(value, NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out int number))
                    {
                        Console.Error.WriteLine($"valor inválido \"{value}\" para -{name}: erro de conversão");
                        Console.Error.WriteLine(Usage);
                        return false;
                    }
                    if (name == "processes")
                    {
                        options.Processes = number;
                    }
                    else
                    {
                        options.Expected = number;
                    }
                    break;
                default:
                    Console.Error.WriteLine($"flag não reconhecida: -{name}");
                    Console.Error.WriteLine(Usage);
                    return false;
            }
        }

        exitCode = 0;
        return true;
    }
}
