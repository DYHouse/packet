# 金额单位统一重构 - 完成总结

## 重构目标

统一系统中金额单位处理：平台 API 用元（字符串），游戏服务用分（int64），回传前端时需要手动 /100 转换。引入 `currency.Money` 类型，在 JSON 序列化边界自动输出元值，消除散落的转换代码。

## 修改文件清单

### 新增文件
| 文件 | 说明 |
|------|------|
| `backend/common/currency/money.go` | `Money` 类型定义，实现 MarshalJSON/UnmarshalJSON 自动分→元转换 |
| `backend/common/currency/convert.go` | 迁移自 `api/platform/utils.go` 的 ParseAmount/FormatAmount 函数 |
| `backend/common/currency/money_test.go` | 单元测试，覆盖正/零/负数、往返序列化、结构体嵌入等场景 |

### 修改文件
| 文件 | 修改内容 |
|------|---------|
| `backend/api/platform/utils.go` | FormatAmount/ParseAmount 委托到 currency 包，现有调用方零改动 |
| `backend/common/message/payload.go` | 12 个金额字段从 `int64` 改为 `currency.Money`，JSON 输出自动为元 |
| `backend/game/application/game_app_service.go` | 消息构造处添加 `currency.NewMoneyFromFen()` 转换，事件发布处用 `.Fen()` 回转 |
| `backend/stats/dto/stats_dto.go` | 13 个金额字段从 `int64` 改为 `currency.Money` |
| `dashboard/src/utils/format.js` | `formatMoney` 适配元值输入，`toYuan` 兼容保留 |
| `dashboard/src/views/Dashboard.vue` | 移除 `toYuan()` 转换调用（值已是元） |

## 核心设计

- **内部保持分为单位**：domain/model 层全部保持 `int64` 分值不变
- **输出边界自动转换**：`currency.Money` 的 `MarshalJSON` 将分自动输出为元值（如 1250 → 12.50）
- **输入边界自动解析**：`UnmarshalJSON` 同时支持数值和字符串输入
- **向后兼容**：`platform.FormatAmount`/`platform.ParseAmount` 通过变量委托保持 API 不变

## 验证结果

- `go build` 全部核心包编译通过
- `go test ./common/currency/...` 单元测试通过
- `go vet` 静态分析无问题

## 未修改（按用户要求）

- `backend/test-ws.html`：测试页面中的 `/ 100` 转换暂不修改
