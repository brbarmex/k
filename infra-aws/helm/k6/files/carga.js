// Carga da POC na AWS (03_kubernetes_e_carga.md, seção 3.1). Roda no pod do k6 do
// cluster 1 e escreve nas células pelo DNS privado; a follower encaminha para a líder,
// então ninguém redireciona nada à mão quando a líder cai.
//
// Variáveis (k6 run -e NOME=valor):
//   PERFIL   constante | rampa                       (padrão constante)
//   TPS      taxa do perfil constante                 (padrão 500)
//   DURACAO  duração do perfil constante             (padrão 3m)
//   INICIO, PASSO, PASSO_DURACAO, MAX                 rampa: de INICIO a MAX, +PASSO a cada PASSO_DURACAO
//                                                    (padrão 100, 100, 30s, 2000)
//   ENTRADA  todas | lider | follower | 1 | 2 | 3     (padrão todas)
//   CENARIO  nome gravado no ledger                   (padrão sem-nome)
//
// Ledger (ORC-01): cada requisição vira uma amostra da métrica dr_ledger com cenário,
// célula e status nas tags, e pk, x-dr-index, x-dr-code, erro e envio nos METADADOS.
// Tag única por requisição vira uma série nova que o k6 nunca libera: a 300 TPS o pod
// passava de 1 GiB e era despejado. Metadados saem no --out json sem criar série.
// Rode com --out json=/out/<cenario>.json e filtre metric == "dr_ledger" no oráculo.

import exec from "k6/execution";
import http from "k6/http";
import { Trend } from "k6/metrics";

const CELULAS = [1, 2, 3];
const PERFIL = __ENV.PERFIL || "constante";
const TPS = Number(__ENV.TPS || 500);
const DURACAO = __ENV.DURACAO || "3m";
const INICIO = Number(__ENV.INICIO || 100);
const PASSO = Number(__ENV.PASSO || 100);
const PASSO_DURACAO = __ENV.PASSO_DURACAO || "30s";
const MAX = Number(__ENV.MAX || 2000);
const ENTRADA = __ENV.ENTRADA || "todas";
const CENARIO = __ENV.CENARIO || "sem-nome";

const ledger = new Trend("dr_ledger", true);

const host = (cell) => `cell-${cell}-0.cell-${cell}.dr.internal`;

function cenarioK6() {
  if (PERFIL === "rampa") {
    const stages = [];
    for (let alvo = INICIO; alvo <= MAX; alvo += PASSO) {
      stages.push({ target: alvo, duration: "1s" }, { target: alvo, duration: PASSO_DURACAO });
    }
    return {
      executor: "ramping-arrival-rate",
      startRate: INICIO,
      timeUnit: "1s",
      stages,
      preAllocatedVUs: Math.ceil(INICIO / 5),
      maxVUs: 2000,
      exec: "putItem",
    };
  }
  return {
    executor: "constant-arrival-rate",
    rate: TPS,
    timeUnit: "1s",
    duration: DURACAO,
    preAllocatedVUs: Math.ceil(TPS / 5),
    maxVUs: Math.max(100, TPS * 2),
    exec: "putItem",
  };
}

export const options = {
  scenarios: { carga: cenarioK6() },
  summaryTrendStats: ["avg", "p(90)", "p(99)", "p(99.9)", "max"],
  // Só os tags de sistema necessários: o JSON do ledger fica menor a 500 TPS.
  systemTags: ["status", "method", "name", "scenario", "error_code"],
  thresholds:
    PERFIL === "rampa"
      ? { http_req_failed: [{ threshold: "rate<0.01", abortOnFail: true, delayAbortEval: "30s" }] }
      : {},
};

// Descobre a líder pelo /status de cada célula (porta de administração 9090).
export function setup() {
  let lider = 0;
  for (const cell of CELULAS) {
    const res = http.get(`http://${host(cell)}:9090/status`, { timeout: "3s" });
    if (res.status === 200 && res.json("papel") === "lider") {
      lider = cell;
    }
  }
  let alvos;
  if (ENTRADA === "todas") alvos = CELULAS;
  else if (ENTRADA === "lider") alvos = [lider];
  else if (ENTRADA === "follower") alvos = CELULAS.filter((c) => c !== lider);
  else alvos = [Number(ENTRADA)];
  if (alvos.some((c) => !CELULAS.includes(c))) {
    throw new Error(`ENTRADA=${ENTRADA} sem alvo válido (líder vista: ${lider || "nenhuma"})`);
  }
  console.log(`cenario=${CENARIO} perfil=${PERFIL} lider=${lider} alvos=${alvos.join(",")}`);
  return { alvos };
}

// CPF sintético com os dois dígitos verificadores válidos. Nunca usar CPF real.
function cpfSintetico() {
  const d = [];
  for (let i = 0; i < 9; i++) d.push(Math.floor(Math.random() * 10));
  for (let n = 9; n <= 10; n++) {
    let soma = 0;
    for (let i = 0; i < n; i++) soma += d[i] * (n + 1 - i);
    const resto = (soma * 10) % 11;
    d.push(resto === 10 ? 0 : resto);
  }
  return d.join("");
}

const NOMES = ["Ana", "Bruno", "Carla", "Diego", "Elisa", "Fabio", "Gabriela", "Hugo"];

export function putItem(data) {
  const cell = data.alvos[exec.scenario.iterationInTest % data.alvos.length];
  // Uma pk nova por escrita (regra do CRG): sem isso, sobrescrita e perda não se distinguem.
  const pk = `cli#${CENARIO}-${exec.vu.idInTest}-${exec.scenario.iterationInTest}-${Date.now()}`;
  const corpo = JSON.stringify({
    TableName: `cadastro_cliente_cell_${cell}`,
    Item: {
      pk: { S: pk },
      nome: { S: NOMES[exec.scenario.iterationInTest % NOMES.length] },
      cpf: { S: cpfSintetico() },
    },
  });

  const enviado = Date.now();
  const res = http.post(`http://${host(cell)}:8000`, corpo, {
    headers: {
      "Content-Type": "application/x-amz-json-1.0",
      "X-Amz-Target": "DynamoDB_20120810.PutItem",
    },
    timeout: "10s",
    tags: { name: "PutItem", celula: String(cell) },
  });

  const meta = exec.vu.metrics.metadata;
  meta.pk = pk;
  meta.dr_index = res.headers["X-Dr-Index"] || "";
  meta.dr_code = res.headers["X-Dr-Code"] || "";
  meta.erro = res.error_code ? String(res.error_code) : "";
  meta.enviado = String(enviado);
  ledger.add(res.timings.duration, {
    cenario: CENARIO,
    celula: String(cell),
    status: String(res.status),
  });
  for (const k of ["pk", "dr_index", "dr_code", "erro", "enviado"]) delete meta[k];
}
