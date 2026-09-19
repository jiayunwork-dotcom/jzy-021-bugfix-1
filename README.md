# cpm-scheduler — 关键线路法进度计算内核

仅做一件事：接收一张工序网络（标识、历时、紧前），完成 CPM 进度计算并落
成一份可按编号取回的「计划作业」。只经 HTTP 对外，无甘特图、无协作、无账
户。作业以本地 JSON 文件保存，不启动数据库进程。

## 计算规则

- **虚拟源点 / 汇点**：无紧前的工序从虚拟源点出发，无紧后的工序进入虚拟
  汇点；二者只用于闭合网络，不会出现在关键线路名单里。
- **正推**：`ES = max(紧前 EF)`，`EF = ES + 历时`；并行两支在汇合点取较大
  `EF`。汇点 `EF` 即项目工期。
- **反推**：汇点工序 `LF = 项目工期`，其余 `LF = min(紧后 LS)`，
  `LS = LF - 历时`。
- **总时差**：同时要求 `TF = LS - ES` 与 `TF = LF - EF`（内核断言两式相
  等，不静默信任）。**自由时差** `FF = min(紧后 ES) − EF`，恒满足
  `0 ≤ FF ≤ TF`。
- **关键线路**：总时差为 0 的工序必须串成源→汇的通路；有多条时全部列
  出，名单中只有调用方提交的标识。
- **历时**：确定历时（正数天数），或乐观/最可能/悲观三点。三点必须
  `0 < O ≤ M ≤ P`。期望 `(O+4M+P)/6`，方差 `((P−O)/6)^2`。零历时「里程
  碑」不是合法工序。
- **PERT**：项目方差沿一条选定关键线路求和（确定历时工序贡献 0，不编造
  方差）；多条关键线路时取**方差最大**的一条，结果中标明选了哪一条，并
  列出每条关键线路的方差。
- **完工概率**：给了目标日期时做正态近似，公式写在结果的
  `probability_formula` 字段：
  `P = Φ((target − mean)/stddev)`，`Φ(z)=½(1+erf(z/√2))`；目标早于均
  值时概率小于一半；未给目标日期时概率字段留空。

整网拒绝计算的情况：空网络、标识缺失/重复、未知紧前、自指紧前、有向
环、历时非正、三点顺序颠倒或缺项、同时给两种历时。错误均带 `kind`。

## HTTP 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/jobs` | 提交网络，计算并保存，返回作业（201） |
| `GET` | `/jobs/{n}` | 按编号取回作业 |
| `GET` | `/jobs` | 列出全部作业编号 |
| `GET` | `/demo` | 查看内置并行土建示范网络与手算核对值 |
| `POST` | `/demo/jobs` | 把示范网络计算并落成作业 |
| `GET` | `/healthz` | 存活探针 |

错误体形如 `{"error":{"kind":"DIRECTED_CYCLE","message":"..."}}`，类型取
自：`EMPTY_NETWORK / MISSING_ID / DUPLICATE_ID / UNKNOWN_PREDECESSOR /
SELF_PRECEDENCE / DIRECTED_CYCLE / INVALID_DURATION /
INVALID_THREE_POINT / MISSING_ESTIMATE / INVALID_TARGET`。

```bash
go run ./cmd/cpm-server -addr :8080 -data ./data

curl -s localhost:8080/demo | jq .hand_check
curl -s -X POST localhost:8080/jobs -H 'Content-Type: application/json' \
  --data @examples/deterministic.json | jq .
curl -s localhost:8080/jobs/1 | jq .
```

三点历时 + 目标日期见 `examples/pert.json`。

## 作业落盘内容

`data/job-N.json`：工序、紧前/紧后、每道工序的 ES/EF/LS/LF、总时差与自
由时差、是否关键、全部关键通路、项目工期；有三点历时与目标日期时另存期
望均值、各关键线路方差、所选关键通路、标准差与完工概率及所用近似公式。
文件先写临时文件再 rename，并发提交由互斥锁分配单调编号；每份作业独立成
文件，互不渗透。

## 模块拆分（职责分离）

- `validate.go` — 入参检查（标识/紧前/历时/三点）
- `topo.go` — Kahn 拓扑排序与有向环检测
- `schedule.go` — 正推、反推、总时差/自由时差（含两式相等断言）
- `paths.go` — 关键通路枚举、PERT 选路与方差、正态近似
- `demo.go` — 内置土建示范网络（可手算核对）
- `store/` — 本地文件库
- `httpapi/` — 仅 HTTP 传输层
- `cmd/cpm-server/` — 进程入口

## 测试锁定的关系

`go test ./...` 覆盖：时差两式相等且整数历时时差恰为 0/正整数、
FF ≤ TF、关键通路贯穿虚拟源到汇且不含虚拟节点、有向环被拒、未知紧前/自
指/重复标识/空标识被拒、非正历时与零里程碑被拒、三点颠倒被拒、非关键边
改宽度项目方差不变、关键边改宽度项目方差必变、多关键通路取方差最大者、
目标早于均值概率 < ½、40 份网络并发落盘互不渗透、HTTP 类型化错误。

## 容器

```bash
docker build -t cpm-scheduler .
docker run -p 8080:8080 -v "$PWD/data:/data" cpm-scheduler
```

镜像用 `golang:1.22` 构建静态二进制并在同系镜像中以单容器运行（无数据
库进程）。容器内以 `nobody` 运行；若用宿主机绑定挂载且目录属主不是
65534(nobody)，可改用具名卷（继承镜像内 `/data` 属主）或加
`--user "$(id -u):$(id -g)"`。
