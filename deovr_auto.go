package main

import (
	"context"
	"errors"
	"time"
)

func (a *App) stopDeoAuto() {
	a.mu.Lock()
	cancel, done := a.deoAutoCancel, a.deoAutoDone
	a.deoAutoCancel = nil
	a.deoAutoDone = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func (a *App) startDeoAuto() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.mu.Lock()
	a.deoAutoCancel = cancel
	a.deoAutoDone = done
	mode := a.deoConnectionMode
	a.mu.Unlock()
	go func() {
		defer close(done)
		connectedHost := ""
		for {
			if ctx.Err() != nil {
				return
			}
			host := "127.0.0.1"
			if mode == "remote" {
				host = a.media.VideoClient()
			}
			state := a.deo.Snapshot()
			if host == "" {
				a.deo.Waiting("等待头显请求视频")
			} else if !state.Connected || host != connectedHost {
				if state.Connected {
					a.deo.Disconnect()
				}
				message := "正在连接本地 DeoVR"
				if mode == "remote" {
					message = "正在连接请求视频的头显"
				}
				a.deo.Waiting(message)
				err := a.deo.Connect(host)
				if ctx.Err() != nil {
					a.deo.Disconnect()
					return
				}
				if err == nil {
					connectedHost = host
				} else {
					a.deo.Waiting("进度接口未连接，正在重试")
				}
			}
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}
func (a *App) SetDeoConnectionMode(mode string) error {
	if mode != "local" && mode != "remote" {
		return errors.New("连接模式无效")
	}
	a.stopDeoAuto()
	a.Stop()
	a.deo.Disconnect()
	a.media.Close()
	if mode == "remote" && a.isDeo() {
		if err := a.media.StartLAN(); err != nil {
			return err
		}
		a.refreshDeoCatalog()
	}
	a.mu.Lock()
	a.deoConnectionMode = mode
	a.mu.Unlock()
	if a.isDeo() {
		a.startDeoAuto()
	}
	return nil
}
