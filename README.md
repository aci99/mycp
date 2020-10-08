# mycp - simple scp

# 简介
一个简单的文件传输工具. 其初衷是为了方便将本地修改的代码上传到服务器进行编译测试 (背景: scp 使用受限).

# 特点

1. 支持只传输自上次传输过后修改过的文件.
2. 支持传输文件夹.
3. 支持认证 (authentication), 密文形式传输.

# 注意

1. 仅在 Windows 之间, Linux 之间以及 Windows 和 Linux 之间测试过, 未在 MacOS 上测试过.
2. 暂时不支持软链接.
3. 暂时未对传输的文件进行切片, 所以单个文件超过 500MB 或者单个文件传输时长超过 30s 会失败.
4. 暂时未处理目标路径是源路径下的一个子路径的情况, 如果出现这种情况, 会导致无穷递归.

# 使用

## 编译

执行

``` bash
cd mycp/cmd
go install ./...
```

在 ${GOBIN} 下会生成两个可执行文件 mycp, mycpserver

## 启动服务端

在一台机器上启动 mycpserver, 并指定监听端口为 31002

``` bash
mycpserver  --host=0.0.0.0:31002 # 如果不指定 host 则默认为 0.0.0.0:31001
```

> 2020/10/06 20:22:10.036750 main.go:21: password=>"Lu8EGLnS2flCK6fA"
> 2020/10/06 20:22:10.037153 mycpserver.go:85: listening on 0.0.0.0:31002

日志会显示此次 server 端的密码为 `Lu8EGLnS2flCK6fA`

## 使用客户端

### 基本使用

在另一台机器上使用 mycp 传输文件/文件夹.

``` bash
mycp --src=@ip:port:src/path --dst=dst/path --password=Lu8EGLnS2flCK6fA --modified=false
mycp --src=src/path --dst=@ip:port:dst/path --password=Lu8EGLnS2flCK6fA --modified=true
```

### 规则

1. `@ip:port:path` 指示远端路径, 必须有且只能有一个远端路径. 可以用 `@R` 代替 `@ip:port`, 表示最近一次 mycp 使用的 ip:port.
2. 如果 src/path 对应的是路径, 则会传输这个路径并递归地传输其包含的所有子路径以及文件.
3. 如果 dst/path 需要的路径不存在, 则会创建.
4. windows 路径一律使用 '/', 比如 `mycp --src=@192.168.1.2:D:/path/to/file --dst=...`
5. `--modified=true` 表示只拷贝自上次 mycp 后修改过的文件. 具体规则是, 每次 mycp 成功执行后, 会持久化一个 `--src=[@ip:port:]path` => `这次 mycp 的开始时间` 键值对, 当下次再 mycp 相同的源路径, 那么只会传输该源路径下修改时间晚于之前持久化的 `这次 mycp 的开始时间` 的文件, 需要传输的文件夹不受这个时间限制.
6. 以 `mycp --src=srcpath --dst=dstpath ...` 为例
   1. 如果 srcpath 是文件
      1. 如果 dstpath 存在且是文件, 则文件 srcpath 覆盖文件 dstpath
      2. 如果 dstpath 存在且是路径, 则文件 srcpath 拷贝至路径 dstpath 下
      3. 如果 dstpath 不存在, 且不以 '/' 结尾, 则创建文件 dstpath, 然后文件 srcpath 覆盖文件 dstpath
      4. 如果 dstpath 不存在, 且以 '/' 结尾, 则文件 srcpath 拷贝至路径 dstpath 下
   2. 如果 srcpath 是路径
      1. 如果 dstpath 存在且是文件, 则报错
      2. 其他: 将路径 srcpath 拷贝至 dstpath 下. 比如 `mycp --src=p1/p2 --dst=@ip:port:p3/p4 ...` 最终得到的是 p3/p4/p2

### 更方便的使用

为了方便使用, 并且解决命令中写有密码会导致密码泄露的问题, 可以这样

``` bash
#/bin/bash

read -s MYCPPWD
export MYCPPWD

function mcp() {
    if [ $# -lt 2 ]; then
        echo 'Usage: mcp <srcpath> <dstpath> [-m]'
        return 1
    fi

    if [ "$3" = "-m" ]; then
        mycp --src="$1" --dst="$2" --password="${MYCPPWD}" --modified=true
    else
        mycp --src="$1" --dst="$2" --password="${MYCPPWD}" --modified=false
    fi
}
```

最终的使用方式为

``` bash
mcp @ip:port:src/path dst/path
mcp src/path @ip:port:dst/path -m
```

# 其他

## 服务端密码

如果在可执行文件 mycpserver 所在路径下存在文件 *mycp_password.txt*, 则启动 mycpserver 时会加载该文件的内容并将其作为密码. 注意密码必须是 16 个英文字符或者数字.

注意: mycpserver 自启动后, 如果累计出现 5 次密码错误, mycpserver 进程会自动挂掉.

## mycp 所需信息的持久化

最近一次的 remote host 以及众多的键值对 `--src=[@ip:port:]path` => `这次 mycp 的开始时间`, 是持久化在可执行文件 mycp 所在路径下的 *mycp_info.txt* 文件里, 其内容以 json 字符串的形式存储. 该文件只增不减, 如果该文件所包含的字节数超过 100MB, 再执行 mycp 时会失败. 如果出现这种情况, 删除该文件即可正常使用.

