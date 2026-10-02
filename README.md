# Modbus TCP 只读采集后端

按点位名批量读取仪表数据的 HTTP 服务。纯 Go 1.27 + Chi 5.2.1，无其他依赖。

## 构建与启动

```bash
go build -o bin/modbus-backend .
./bin/modbus-backend -listen :8080 -config config.json -max-parallel 8 -with-sim
```

- `-with-sim`（默认开）在本机 `:1502` 启动一个示例寄存器设备（unit 1），无需硬件即可演示。
- `-max-parallel` 限制同时读取的设备数；同一设备的请求始终串行。

## HTTP 接口

### PUT /api/config —— 整表替换配置（原子持久化，版本递增）

```json
{
  "devices": [{"id":"meter1","address":"127.0.0.1:1502","unit_id":1,"timeout_ms":2000}],
  "points": [
    {"device_id":"meter1","name":"voltage","func_code":3,"address":0,"type":"u16","scale":0.1,"offset":0},
    {"device_id":"meter1","name":"pressure","func_code":3,"address":10,"type":"f32","word_order":"high_first","scale":1,"offset":0}
  ]
}
```

- 设备：唯一 `id`、TCP `address`、`unit_id`、`timeout_ms`。
- 点位：`device_id` 引用、唯一 `name`、`func_code` 仅 03/04、`address` 从 0 起、
  `type` 为 `u16`/`i16`/`f32`、`word_order` 为 `high_first`/`low_first`（f32 必填）、
  `scale`/`offset` 必须为有限数且 scale 非零。
- 校验拒绝：悬空设备引用、重复 ID/名称、寄存器范围越过 65535、非有限数值。
- 校验或落盘失败时保持旧版本；写入采用临时文件 + rename 原子替换，重启自动恢复。

### GET /api/config —— 查看当前配置与版本

### POST /api/collect —— 按点位名批量采集

```bash
curl -X POST localhost:8080/api/collect -d '{"names":["voltage","pressure"]}'
```

```json
{"version":1,"results":[
  {"name":"voltage","ok":true,"raw":2345,"engineering":234.5},
  {"name":"pressure","ok":true,"raw":3.141590118408203,"engineering":3.141590118408203}
]}
```

- 每次采集固定使用同一配置快照，结果携带 `version`。
- 部分失败逐点给出 `error`，成功点保留真实值（HTTP 207）；全部失败返回 502。
- 不用零值或旧缓存冒充结果；f32 解码出 NaN/Inf 或工程值溢出均明确失败。

## 读取规划与协议

- 同设备同功能码的重叠/相邻寄存器范围自动合并，单请求最多 125 寄存器，拆分绝不切断点位。
- 每次采集对每台设备新建连接，串行执行该设备的请求；不同设备并行（受 `-max-parallel` 限制）。
- 完整校验 MBAP：事务 ID、协议 ID、长度、Unit ID、功能码、字节数；识别异常响应（0x80|fc）。
- `io.ReadFull` 处理 TCP 分段与粘包；客户端取消或超时立即关闭连接并释放并发额度。
- 寄存器内字节大端；f32 支持高字/低字在前；i16 按补码解释；工程值 = raw × scale + offset。

## 代码结构

| 包 | 职责 |
|---|---|
| `config` | 配置模型、校验、原子持久化、版本管理 |
| `plan` | 读取范围合并与拆分（≤125 寄存器） |
| `modbus` | Modbus TCP 客户端、MBAP 校验、异常识别 |
| `values` | 字节序/补码/IEEE754 解码、比例偏移、有限性检查 |
| `collect` | 采集调度：设备并行、同设备串行、快照固定 |
| `api` | Chi HTTP 接口 |
| `sim` | 本机示例寄存器设备（测试与演示） |

## 测试

```bash
go test ./...
```

覆盖：配置校验/持久化/并发串行提交、范围合并拆分、协议帧校验（事务/Unit ID/字节数/异常/分段）、
取消及时释放、端到端采集与部分失败。
