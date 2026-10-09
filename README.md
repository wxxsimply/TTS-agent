# TTS Data Agent (Go)

企业级智能报表与数据分析 Agent（Text-to-SQL + Data Agent）。Go 后端 + React 前端，面向跨境电商独立站场景。

## 架构

```
用户提问 → Schema 路由(pgvector) → Few-Shot 检索(BM25)
  → PostgreSQL Job Queue → Worker Lease/Heartbeat → Graph Runtime
      → GenerateSQLNode → ValidateSQLNode → ExecuteSQLNode → ReportNode → ChartNode → End
              ↑____________ 校验/执行失败后的有限修正循环 ____________↓
```

Graph Runtime 负责节点注册、条件路由、最大尝试次数、异常终止和步骤追踪。每次运行会生成 `run_id`，并把运行状态、Graph 版本及节点级路由、尝试次数、耗时、错误分类持久化到 `agent_runs` / `agent_steps`。节点提交时会原子写入 `agent_checkpoints`；查询结果和报告被隔离在 `agent_run_artifacts`，不会混入 Checkpoint。系统支持同一 `run_id` 的版本校验、断点恢复和幂等步骤序号，不保存 Prompt、模型原始输出或凭据。

受 JWT 保护的运行生命周期接口：

- `GET /api/agent-runs/:id`：查看运行状态和最新 Checkpoint 序号
- `GET /api/agent-runs/:id/steps`：查看安全的节点执行轨迹
- `POST /api/agent-runs/:id/resume`：从最新 Checkpoint 恢复中断运行

恢复接口拒绝其他用户、已完成任务、仍活跃的任务、不可恢复的业务失败和不兼容的 Graph 版本。超过 5 分钟未更新的 `running` 任务会被识别为中断任务。

异步任务接口：

- `POST /api/agent-runs`：幂等提交后台分析任务，立即返回 `202 + run_id`
- `POST /api/agent-runs/:id/cancel`：取消排队中或运行中的任务
- `POST /api/agent-runs/:id/retry`：从最后一个可用 Checkpoint 重新入队
- `GET /api/agent-runs/:id/result`：读取已完成任务的报告与结果预览
- `GET /api/agent-jobs/stats`：查看当前用户的队列、成功率和耗时指标

`cmd/worker` 使用 `FOR UPDATE SKIP LOCKED` 抢占任务，并通过租约、心跳和任务尝试号 fencing 防止旧 Worker 重复提交。基础设施错误按指数退避自动重试，安全拒绝和配置错误不会自动重试。API 与 Worker 可独立扩容，现有 `/api/chat/query` 同步接口继续保留兼容。

## 技术栈

| 层 | 技术 |
|---|---|
| Backend | **Go 1.22 + Gin + pgx** |
| Agent | 自研 Agent Graph Runtime + 类型化 State/Node |
| LLM | DeepSeek（OpenAI 兼容 API） |
| Database | PostgreSQL 16 + pgvector |
| Frontend | React + Vite + Ant Design + ECharts |

## 快速开始

### 1. 配置环境

```bash
cp .env.example .env
# 编辑 .env，填入 DEEPSEEK_API_KEY
```

### 邮箱验证码登录

系统同时支持邮箱验证码登录和密码登录。默认 `EMAIL_PROVIDER=log`，不会向真实邮箱投递，验证码接口会明确提示邮件服务尚未配置；这是为了避免本地环境误以为邮件已经发出。

生产环境可使用任意支持 SMTP 的邮件服务，不绑定单一供应商：

```env
EMAIL_PROVIDER=smtp
EMAIL_FROM=经营分析助手 <no-reply@your-domain.com>
EMAIL_SMTP_HOST=smtp.your-provider.com
EMAIL_SMTP_PORT=587
EMAIL_SMTP_TLS_MODE=starttls
EMAIL_SMTP_USERNAME=your-smtp-username
EMAIL_SMTP_PASSWORD=your-smtp-password
```

若服务商使用 465 端口，请设置 `EMAIL_SMTP_TLS_MODE=implicit`。生产环境需要先在邮件服务商处验证发信域名，并配置 SPF、DKIM。

### 2. Docker 一键启动

```bash
docker compose up --build
```

- 前端: http://localhost:3000
- 后端 API: http://localhost:8000
- 健康检查: http://localhost:8000/api/health
- 存活检查: http://localhost:8000/api/health/live
- 就绪检查: http://localhost:8000/api/health/ready

### 3. 本地开发

**后端**

```bash
# 启动 PostgreSQL + Redis（可用 docker compose up postgres redis -d）
go run ./cmd/server
```

**前端**

```bash
cd frontend
npm install
npm run dev
# 访问 http://localhost:5173
```

## API 端点

- `POST /api/auth/register` - 注册
- `POST /api/auth/login` - 登录
- `POST /api/auth/email-code` - 发送邮箱登录验证码
- `POST /api/auth/email-login` - 使用邮箱验证码登录或自动注册
- `POST /api/auth/refresh` - 刷新 Token
- `POST /api/chat/query` - 自然语言查询
- `GET /api/conversations` - 会话列表
- `GET /api/export/:message_id/excel` - Excel 导出
- `GET /api/datasets` - 当前用户的数据集列表
- `POST /api/datasets/import` - 导入 `.xlsx` 数据集（最大 10MB、50,000 行）
- `DELETE /api/datasets/:id` - 删除当前用户的数据集
- `POST /api/webhook/feishu/query` - 飞书 Bot Webhook

## 导入 Excel 分析

1. 登录后点击页面右上角的“导入 Excel”。
2. 上传 `.xlsx` 文件；第一行必须是字段名称，系统会自动识别数值、日期、布尔值和文本类型。
3. 在“当前数据源”中选择刚导入的数据集，再用自然语言提问。
4. 每个分析会话会固定绑定创建时的数据源。需要切换文件时，请从顶部切换数据源并新建分析。

项目内置了 [销售数据示例.xlsx](examples/销售数据示例.xlsx)，可直接用于体验。导入数据通过 PostgreSQL 行级安全策略按用户隔离，Agent 仍然只使用只读连接执行经过校验的 `SELECT`。

## 测试

```bash
go test ./... -v
go run ./cmd/eval   # 需要 DEEPSEEK_API_KEY 和数据库
```

## 项目结构

```
TTS-agent/
├── cmd/server/           # Go 服务入口
├── cmd/eval/             # 准确率评估
├── internal/
│   ├── agent/            # Graph Runtime、State、Node 与执行器
│   ├── api/              # Gin HTTP handlers
│   ├── auth/             # JWT 认证
│   ├── schema/           # pgvector Schema 路由
│   ├── fewshot/          # BM25 检索
│   ├── security/         # SQL 安全校验
│   └── domain/           # 可插拔垂直领域
├── domains/ecommerce/    # DDL + metadata + fewshot
├── frontend/             # React 前端
└── docker-compose.yml
```

## 简历亮点

- Go 高并发后端：Gin + pgx 连接池，单二进制部署
- 自研 Agent Graph Runtime：节点注册、条件路由、有限修正循环、终止保护，以及可按 `run_id` 追溯的持久化运行记录
- pgvector 动态 Schema 路由 + BM25 Few-Shot 提升复杂 SQL 准确率
- 读写分离 + Statement Timeout + sqlguard 危险语句防护
- JWT 多设备会话同步，可插拔垂直领域 Schema

## 云端部署

```bash
git clone <repo>
cp .env.example .env
docker compose up -d --build
```

推荐使用 Caddy/Nginx 反代并配置 HTTPS（见 `deploy/Caddyfile`）。
