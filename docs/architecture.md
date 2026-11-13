# 架构说明

本项目是干净微服务框架，默认只保留 HTTP gateway、配置、日志、中间件、数据库和外部组件封装，不包含业务 demo。

## 分层建议

新增服务时按以下包名组织：

~~~text
internal/<service>/entity   # 业务实体、业务错误、仓储接口
internal/<service>/model    # 数据库表结构和文档结构
internal/<service>/dto/command.go      # 业务用例入参命令
internal/<service>/dto/<service>.go    # 业务用例出参 DTO 和转换
internal/<service>/service/service.go  # 业务流程编排
internal/<service>/repo     # Gorm、MongoDB、Redis、外部依赖实现
internal/<service>/handler  # gRPC/HTTP 入站适配
~~~

依赖方向：

~~~text
handler -> service -> entity
repo -> entity
repo -> model
~~~

entity 不依赖 Gin、gRPC、Gorm 或云 SDK，便于测试和替换基础设施。model 只维护数据库结构，不写查询逻辑。service 继续拆为 command、dto、service 三个文件：command 放业务入参，dto 放业务出参和转换，service 放业务流程。

## Gateway

Gateway 默认包含：

~~~text
cmd/gateway/main.go
internal/gateway/router/router.go
internal/gateway/router/health.go
internal/gateway/router/v1.go
~~~

路由按：

~~~text
版本 -> 业务 -> 具体接口
~~~

的方式扩展，例如：

~~~text
/api/v1/products
/api/v1/orders
~~~

## 公共能力

公共能力在 pkg 下：

~~~text
pkg/config
pkg/logger
pkg/errors
pkg/httpx
pkg/middleware
pkg/grpcx
pkg/database
pkg/mysqlx
pkg/postgresx
pkg/mongox
pkg/redisx
pkg/esx
pkg/kafkax
pkg/filex
pkg/validator
~~~
