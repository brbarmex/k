# infra-aws: POC do DataReplication na AWS com custo mínimo

Infra-as-code do plano aprovado em 23/10/2026 (pasta do projeto `teste_aws_baixo_custo/`: README e arquivos 01 a 07). Três clusters EKS em sa-east-1, um por AZ, cada um com uma célula da POC; DynamoDB on-demand por célula; observabilidade na sua máquina.

**Custo estimado:** ~US$ 0,53 por hora de infra. Uma sessão de ~3,6 h com baseline a 500 TPS e 10 falhas a 100 TPS sai por **~US$ 3,95** (a 500 TPS nas falhas, ~US$ 8,70). Esquecido ligado, ~US$ 380 por mês: por isso a autodestruição.

## O que tem aqui

| Pasta | Conteúdo |
|---|---|
| `terraform/conta/` | Aplicada **uma vez, nunca destruída**: Budgets (US$ 15/mês e US$ 3/dia), detecção de anomalia, SNS, Lambda de autodestruição a cada 15 min |
| `terraform/sessao/` | Por sessão: VPC só com subnets públicas e sem NAT, endpoint gateway do DynamoDB, 3 EKS (`dr-poc-cell-1..3`), node group `cell` (`t4g.medium` on-demand) em cada um, node group `k6` (spot, com taint) no cluster 1, 3 tabelas, zona privada `dr.internal`, Pod Identity do app, do EBS CSI e do ExternalDNS |
| `k8s/base`, `k8s/overlays/cell-N` | StorageClass gp3 (`Delete`, etiquetada), headless `cell-N` com `publishNotReadyAddresses`, StatefulSet `cell-N` (1,5 CPU, 1 GiB, PVC de 10 GiB) |
| `k8s/external-dns` | ExternalDNS por cluster (intervalo 10 s, TTL 5 s), publica `cell-N-0.cell-N.dr.internal` |
| `k8s/k6` | Pod do k6 no nó spot do cluster 1 e o script `carga.js` (perfis constante e rampa, ledger, CPF sintético) |
| `k8s/falhas` | Pod privilegiado para `tc`, `iptables` e `fsfreeze` no nó (injeção de falha sem FIS) |
| `observabilidade/` | Prometheus (`localhost:9096`) e Grafana (`localhost:3001`) locais, com o dashboard da POC e o datasource CloudWatch |
| `scripts/` | `ponte.sh` (port-forwards em laço) e `verificar.sh` (órfãos depois do destroy) |
| `Taskfile.yaml` | Todas as etapas da sessão |

## Pré-requisitos (uma vez)

- Terraform ≥ 1.9, AWS CLI v2 com credencial de um usuário ou SSO com MFA (nunca root), kubectl, Task v3, Docker.
- `terraform/conta/terraform.tfvars` com `email_alertas` (modelo em `terraform.tfvars.example`), depois `task conta:up` e confirmar a assinatura do SNS no e-mail.
- Service Quotas em sa-east-1: on-demand padrão ≥ 6 vCPU e spot padrão ≥ 2 vCPU.
- Imagem da POC **`linux/arm64`** publicada (GHCR público é grátis). Num PC x86: `docker buildx build --platform linux/arm64 -t ghcr.io/<você>/datareplication-poc:<tag> --push DataReplicationApp`. Anote o digest.
- Recomendado: `infracost breakdown --path terraform/sessao` para confirmar o preço por hora.

## Roteiro de uma sessão

```bash
cd infra-aws
task aws:up                         # ~20 min; expira-em = agora + 3,5 h (TTL_HORAS=...)
task aws:kubeconfig                 # contextos cluster-1..3
task k8s:up IMAGE=ghcr.io/...@sha256:...
task k8s:pronto                     # 3 células prontas e /status visto pelo DNS privado
task obs:up                         # Prometheus e Grafana locais
task obs:ponte                      # em outro terminal; deixa aberto

task k6:rodar CENARIO=rampa PERFIL=rampa MAX=2000     # acha o joelho (para sozinho com erro > 1%)
task k6:rodar CENARIO=baseline TPS=500 DURACAO=3m
task k6:rodar CENARIO=fp03 TPS=100 DURACAO=7m          # e a falha em outro terminal
task k6:coletar CENARIO=fp03                           # ledger, resumo, logs, eventos, top

task aws:estender HORAS=1           # se precisar de mais tempo

task k8s:down                       # ANTES do destroy: apaga os PVCs enquanto o driver EBS existe
task aws:down
task aws:verificar                  # tem de terminar em "Limpo."
```

## Cuidados que o código já trata (e não devem ser desfeitos)

- **Sem `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`DYNAMODB_ENDPOINT` no manifesto.** Chave estática ganha do Pod Identity; o DynamoDB real recusa e a POC para o applier como erro permanente.
- **`publishNotReadyAddresses: true`** no headless: sem ele a eleição nunca acontece na partida a frio.
- **Sem taint nos nós de célula**: nos clusters 2 e 3 é o único nó, e CoreDNS, EBS CSI, ExternalDNS e metrics-server rodam nele.
- **`fsGroup: 10001`** no pod: o volume EBS nasce do root e o app roda como 10001.
- **Nada de `Service type: LoadBalancer`**, NAT, KMS ou log do plano de controle: cada um cobra por hora ou fica órfão.
- **Ordem de destruição:** `k8s:down` antes de `aws:down`. Se o volume ficar órfão, a autodestruição apaga volumes soltos com a etiqueta quando não sobra cluster da POC.

## Autodestruição

A stack `sessao` grava a etiqueta `expira-em` em tudo (padrão: agora + 3,5 h). A Lambda da stack `conta` roda a cada 15 min e, para recursos da POC vencidos, apaga node groups, depois os clusters, as tabelas e os volumes soltos, e avisa por e-mail. A VPC e a zona privada sobram (não cobram; a zona é grátis se apagada em até 12 h, então rode `task aws:down` depois). Para testar sem apagar nada, troque `SIMULACAO` para `true` na Lambda.

## O que ainda falta (fora desta pasta)

- **Oráculo do RPO.** Não existe no repositório. Sem ele a sessão mede latência e RTO, mas não prova RPO = 0. Ele lê as 3 tabelas direto no DynamoDB com a sua credencial e confere contra o ledger (`resultados/<cenario>/ledger.json.gz`, métrica `dr_ledger`), pelas regras R1 a R5 do ORC-04 em `validation/00_metodo_e_observabilidade.md`.
- **Validação contra a AWS.** O Terraform passou em `terraform fmt` e `terraform validate`, e os manifestos renderizam com `kubectl kustomize`, mas nada foi aplicado. O primeiro `aws:up` é o teste real; confira `kubectl get pods -A` sem `Pending` nos 3 clusters.
- **ExternalDNS e pods não prontos:** a publicação de `cell-N-0` antes do `/readyz` depende do `publishNotReadyAddresses`; o `task k8s:pronto` mostra se o DNS resolveu.
