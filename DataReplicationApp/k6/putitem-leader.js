import exec from "k6/execution";
import { check, fail } from "k6";
import http from "k6/http";

const cells = [1, 2, 3];

export const options = {
  scenarios: {
    warmup: {
      executor: "constant-arrival-rate",
      exec: "putItem",
      rate: 10,
      timeUnit: "1s",
      duration: "1m",
      gracefulStop: "10s",
      preAllocatedVUs: 20,
      maxVUs: 40,
      tags: { phase: "warmup" },
    },
    ramp_up: {
      executor: "ramping-arrival-rate",
      exec: "putItem",
      startTime: "1m",
      startRate: 10,
      timeUnit: "1s",
      stages: [{ target: 50, duration: "1m" }],
      gracefulStop: "10s",
      preAllocatedVUs: 60,
      maxVUs: 100,
      tags: { phase: "ramp-up" },
    },
    sustained: {
      executor: "constant-arrival-rate",
      exec: "putItem",
      startTime: "2m",
      rate: 100,
      timeUnit: "1s",
      duration: "1m",
      gracefulStop: "10s",
      preAllocatedVUs: 120,
      maxVUs: 200,
      tags: { phase: "sustained" },
    },
  },
  thresholds: {
    checks: ["rate==1"],
  },
};

export function setup() {
  for (const cell of cells) {
    const response = http.get(`http://localhost:909${cell}/status`);
    if (response.status !== 200) {
      continue;
    }

    const status = response.json();
    if (status.papel === "lider") {
      const leaderCell = Number(status.celula);
      if (!cells.includes(leaderCell)) {
        fail(`Unexpected leader cell in /status: ${status.celula}`);
      }
      console.log(`Sending PutItem requests directly to leader cell-${leaderCell}`);
      return { leaderCell };
    }
  }

  fail("No leader found. Start the cells and wait for a leader before running k6.");
}

export function putItem(data) {
  const id = `k6-${exec.scenario.name}-${Date.now()}-${exec.vu.idInTest}-${exec.scenario.iterationInTest}`;
  const payload = {
    TableName: `cadastro_cliente_cell_${data.leaderCell}`,
    Item: {
      pk: { S: id },
      nome: { S: "Maria" },
      sobrenome: { S: "Silva" },
      email: { S: "maria.silva@exemplo.com" },
      contato: { S: "+55 11 91234-5678" },
    },
  };

  const response = http.post(
    `http://localhost:800${data.leaderCell}`,
    JSON.stringify(payload),
    {
      headers: {
        "Content-Type": "application/x-amz-json-1.0",
        "X-Amz-Target": "DynamoDB_20120810.PutItem",
      },
      timeout: "10s",
    },
  );

  check(response, {
    "PutItem returned HTTP 200": (res) => res.status === 200,
  });
}
