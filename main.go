package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// 添加命令行参数解析
func main() {
    // 定义命令行参数
    srcDir := flag.String("src", "D:\\download", "源文件夹路径")
    destDir := flag.String("dest", "D:\\download\\dest", "目标文件夹路径")
    install := flag.Bool("install", false, "安装服务")
    remove := flag.Bool("remove", false, "卸载服务")
    start := flag.Bool("start", false, "启动服务")
    stop := flag.Bool("stop", false, "停止服务")
    flag.Parse()

    if *install {
        err := installService("TsToMp4Service", "TS to MP4 Converter Service")
        if err != nil {
            log.Fatalf("安装服务失败: %v", err)
        }
        log.Println("服务安装成功")
        return
    }

    if *remove {
        err := removeService("TsToMp4Service")
        if err != nil {
            log.Fatalf("卸载服务失败: %v", err)
        }
        log.Println("服务卸载成功")
        return
    }

    if *start {
        err := startService("TsToMp4Service")
        if err != nil {
            log.Fatalf("启动服务失败: %v", err)
        }
        log.Println("服务启动成功")
        return
    }

    if *stop {
        err := stopService("TsToMp4Service")
        if err != nil {
            log.Fatalf("停止服务失败: %v", err)
        }
        log.Println("服务停止成功")
        return
    }

    isIntSess, err := svc.IsAnInteractiveSession()
    if err != nil {
        log.Fatalf("无法判断是否为交互式会话: %v", err)
    }
    if !isIntSess {
        // 作为服务运行
        err = svc.Run("TsToMp4Service", &myService{*srcDir, *destDir})
        if err != nil {
            log.Fatalf("服务运行失败: %v", err)
        }
    } else {
        // 作为控制台应用运行
        run(*srcDir, *destDir)
    }
}

func Run(srcDir string, destDir string) {
    run(srcDir, destDir)
}

// 修改 run 函数，接受 srcDir 和 destDir 参数
func run(srcDir string, destDir string) {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        log.Fatal(err)
    }
    defer watcher.Close()

    // 创建目标目录
    os.MkdirAll(destDir, os.ModePerm)

    err = watcher.Add(srcDir)
    if err != nil {
        log.Fatal(err)
    }

    log.Println("开始监听目录:", srcDir)

    for {
        select {
        case event, ok := <-watcher.Events:
            if !ok {
                return
            }
            if event.Op&fsnotify.Create == fsnotify.Create {
                if strings.HasSuffix(event.Name, ".ts") {
                    log.Println("检测到新文件:", event.Name)
                    // 等待2s，确保文件写入完成
                    time.Sleep(2 * time.Second)
                    go convertTsToMp4(event.Name, destDir)
                }
            }
        case err, ok := <-watcher.Errors:
            if !ok {
                return
            }
            log.Println("错误:", err)
        }
    }
}

func convertTsToMp4(tsPath string, destDir string) {
    fileName := filepath.Base(tsPath)
    mp4Name := strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".mp4"
    destPath := filepath.Join(destDir, mp4Name)

    cmd := exec.Command("ffmpeg", "-i", tsPath, "-c", "copy", destPath)
    err := cmd.Run()
    if err != nil {
        log.Println("转换失败:", err)
    } else {
        log.Println("转换成功:", destPath)
    }
}

// 修改 myService 结构体，添加 srcDir 和 destDir 字段
type myService struct {
    srcDir  string
    destDir string
}

// 修改 Execute 方法，传入 srcDir 和 destDir
func (m *myService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
    const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
    elog, err := eventlog.Open("TsToMp4Service")
    if err != nil {
        // 如果打开事件日志失败，记录错误并退出
        return false, 1
    }
    defer elog.Close()

    elog.Info(1, "服务正在启动")
    changes <- svc.Status{State: svc.StartPending}
    go runWithLogging(m.srcDir, m.destDir, elog)
    changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

    for c := range r {
        switch c.Cmd {
        case svc.Interrogate:
            changes <- c.CurrentStatus
        case svc.Stop, svc.Shutdown:
            elog.Info(1, "服务正在停止")
            changes <- svc.Status{State: svc.StopPending}
            return false, 0
        default:
            elog.Warning(1, fmt.Sprintf("收到未处理的指令: %v", c))
        }
    }
    elog.Info(1, "服务正在停止")
    return false, 0
}

func runWithLogging(srcDir string, destDir string, elog *eventlog.Log) {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        elog.Error(1, fmt.Sprintf("创建文件系统监视器失败: %v", err))
        return
    }
    defer watcher.Close()

    // 创建目标目录
    os.MkdirAll(destDir, os.ModePerm)

    err = watcher.Add(srcDir)
    if err != nil {
        elog.Error(1, fmt.Sprintf("添加监视目录失败: %v", err))
        return
    }

    elog.Info(1, fmt.Sprintf("开始监听目录: %s", srcDir))

    for {
        select {
        case event, ok := <-watcher.Events:
            if !ok {
                return
            }
            if event.Op&fsnotify.Create == fsnotify.Create {
                if strings.HasSuffix(event.Name, ".ts") {
                    elog.Info(1, fmt.Sprintf("检测到新文件: %s", event.Name))
                    // 等待2s，确保文件写入完成
                    time.Sleep(2 * time.Second)
                    go convertTsToMp4WithLogging(event.Name, destDir, elog)
                }
            }
        case err, ok := <-watcher.Errors:
            if !ok {
                return
            }
            elog.Error(1, fmt.Sprintf("监视器错误: %v", err))
        }
    }
}

func convertTsToMp4WithLogging(tsPath string, destDir string, elog *eventlog.Log) {
    fileName := filepath.Base(tsPath)
    mp4Name := strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".mp4"
    destPath := filepath.Join(destDir, mp4Name)

    cmd := exec.Command("ffmpeg", "-i", tsPath, "-c", "copy", destPath)
    err := cmd.Run()
    if err != nil {
        elog.Error(1, fmt.Sprintf("转换失败: %v", err))
    } else {
        elog.Info(1, fmt.Sprintf("转换成功: %s", destPath))
    }
	os.Remove(tsPath)
}

// 注册服务 services.msc 查看注册的服务 安装还要启用服务
func installService(name, desc string) error {
    exepath, err := os.Executable()
    if err != nil {
        return err
    }
    m, err := mgr.Connect()
    if err != nil {
        return err
    }
    defer m.Disconnect()

    s, err := m.OpenService(name)
    if err == nil {
        s.Close()
        return fmt.Errorf("服务 %s 已经存在", name)
    }
    s, err = m.CreateService(name, exepath, mgr.Config{DisplayName: desc, StartType: mgr.StartAutomatic})
    if err != nil {
        return err
    }
    defer s.Close()

    // 确保事件日志的来源名称与服务名称一致
    err = eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info)
    if err != nil {
        s.Delete()
        return fmt.Errorf("安装事件日志失败: %s", err)
    }
    return nil
}

// 卸载服务
func removeService(name string) error {
    m, err := mgr.Connect()
    if err != nil {
        return err
    }
    defer m.Disconnect()

    s, err := m.OpenService(name)
    if err != nil {
        return fmt.Errorf("服务 %s 不存在", name)
    }
    defer s.Close()

    err = s.Delete()
    if err != nil {
        return err
    }

    err = eventlog.Remove(name)
    if err != nil {
        return fmt.Errorf("删除事件日志失败: %s", err)
    }
    return nil
}

// 实现启动服务的函数
func startService(name string) error {
    m, err := mgr.Connect()
    if err != nil {
        return err
    }
    defer m.Disconnect()

    s, err := m.OpenService(name)
    if err != nil {
        return fmt.Errorf("服务 %s 不存在", name)
    }
    defer s.Close()

    err = s.Start()
    if err != nil {
        return fmt.Errorf("启动服务失败: %v", err)
    }

    return nil
}

// 实现停止服务的函数
func stopService(name string) error {
    m, err := mgr.Connect()
    if err != nil {
        return err
    }
    defer m.Disconnect()

    s, err := m.OpenService(name)
    if err != nil {
        return fmt.Errorf("服务 %s 不存在", name)
    }
    defer s.Close()

    status, err := s.Control(svc.Stop)
    if err != nil {
        return fmt.Errorf("停止服务失败: %v", err)
    }

    // 等待服务停止
    timeout := time.Now().Add(20 * time.Second)
    for status.State != svc.Stopped {
        if time.Now().After(timeout) {
            return fmt.Errorf("服务停止超时")
        }
        time.Sleep(300 * time.Millisecond)
        status, err = s.Query()
        if err != nil {
            return fmt.Errorf("查询服���状态失败: %v", err)
        }
    }

    return nil
}