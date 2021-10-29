package huya

import (
	"errors"
	"fmt"
	"github.com/Logiase/MiraiGo-Template/utils"
	"github.com/Sora233/DDBOT/lsp/concern"
	"github.com/Sora233/DDBOT/lsp/concern_type"
	"github.com/Sora233/DDBOT/lsp/mmsg"
	localutils "github.com/Sora233/DDBOT/utils"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"reflect"
	"runtime"
	"sync"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary
var logger = utils.GetModuleLogger("huya-concern")

const (
	Live concern_type.Type = "live"
)

type Concern struct {
	*StateManager

	eventChan chan concernEvent
	notify    chan<- concern.Notify
	stop      chan interface{}
	wg        sync.WaitGroup
}

func (c *Concern) Site() string {
	return "huya"
}

func (c *Concern) ParseId(s string) (interface{}, error) {
	return s, nil
}

func (c *Concern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

func (c *Concern) Stop() {
	logger.Trace("正在停止huya StateManager")
	c.StateManager.Stop()
	logger.Trace("huya StateManager已停止")
	if c.stop != nil {
		close(c.stop)
	}
	close(c.eventChan)
	logger.Trace("正在停止huya concern")
	c.wg.Wait()
	logger.Trace("huya concern已停止")
}

func (c *Concern) Start() error {
	err := c.StateManager.Start()
	if err != nil {
		logger.Errorf("state manager start err %v", err)
	}

	if runtime.NumCPU() >= 3 {
		for i := 0; i < 3; i++ {
			go c.notifyLoop()
		}
	} else {
		go c.notifyLoop()
	}

	go c.EmitFreshCore(Site, func(ctype concern_type.Type, id interface{}) error {
		roomid, ok := id.(string)
		if !ok {
			return fmt.Errorf("cast fresh id type<%v> to string failed", reflect.ValueOf(id).Type().String())
		}
		if ctype.ContainAll(Live) {
			oldInfo, _ := c.FindRoom(roomid, false)
			liveInfo, err := c.FindRoom(roomid, true)
			if err == ErrRoomNotExist || err == ErrRoomBanned {
				logger.WithFields(logrus.Fields{
					"RoomId":   roomid,
					"RoomName": oldInfo.GetName(),
				}).Debugf("直播间不存在或被封禁")
				return nil
			}
			if err != nil {
				return fmt.Errorf("load liveinfo failed %v", err)
			}
			// first load
			if oldInfo == nil {
				liveInfo.LiveStatusChanged = true
			}
			if oldInfo != nil && oldInfo.Living != liveInfo.Living {
				liveInfo.LiveStatusChanged = true
			}
			if oldInfo != nil && oldInfo.RoomName != liveInfo.RoomName {
				liveInfo.LiveTitleChanged = true
			}
			if oldInfo == nil || oldInfo.Living != liveInfo.Living || oldInfo.RoomName != liveInfo.RoomName {
				c.eventChan <- liveInfo
			}
		}
		return nil
	})
	return nil
}

func (c *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	var err error
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("id", id)

	err = c.StateManager.CheckGroupConcern(groupCode, id, ctype)
	if err != nil {
		if err == concern.ErrAlreadyExists {
			return nil, errors.New("已经watch过了")
		}
		return nil, err
	}

	liveInfo, err := c.FindOrLoadRoom(id.(string))
	if err != nil {
		log.Errorf("FindOrLoadRoom error %v", err)
		return nil, fmt.Errorf("查询房间信息失败 %v - %v", id, err)
	}
	_, err = c.StateManager.AddGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	return concern.NewIdentity(liveInfo.RoomId, liveInfo.GetName()), nil
}

func (c *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	id := _id.(string)
	identity, _ := c.Get(id)
	_, err := c.StateManager.RemoveGroupConcern(groupCode, id, ctype)
	return identity, err
}

func (c *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	liveInfo, err := c.FindRoom(id.(string), false)
	if err != nil {
		return nil, err
	}
	return concern.NewIdentity(liveInfo.RoomId, liveInfo.GetName()), nil
}

func (c *Concern) List(groupCode int64, ctype concern_type.Type) ([]concern.IdentityInfo, []concern_type.Type, error) {
	log := logger.WithFields(localutils.GroupLogFields(groupCode))

	_, ids, ctypes, err := c.StateManager.List(func(_groupCode int64, id interface{}, p concern_type.Type) bool {
		return groupCode == _groupCode && p.ContainAny(ctype)
	})
	if err != nil {
		return nil, nil, err
	}
	var resultTypes = make([]concern_type.Type, 0, len(ids))
	var result = make([]concern.IdentityInfo, 0, len(ids))
	for index, id := range ids {
		liveInfo, err := c.FindOrLoadRoom(id.(string))
		if err != nil {
			log.WithField("id", id).Errorf("get LiveInfo err %v", err)
			continue
		}
		result = append(result, concern.NewIdentity(liveInfo.RoomId, liveInfo.GetName()))
		resultTypes = append(resultTypes, ctypes[index])
	}

	return result, resultTypes, nil
}

func (c *Concern) notifyLoop() {
	c.wg.Add(1)
	defer c.wg.Done()
	for ievent := range c.eventChan {
		switch ievent.Type() {
		case Live:
			event := ievent.(*LiveInfo)
			log := event.Logger()
			log.Debugf("new event - live notify")

			groups, _, _, err := c.StateManager.List(func(groupCode int64, id interface{}, p concern_type.Type) bool {
				return id.(string) == event.RoomId && p.ContainAny(Live)
			})
			if err != nil {
				log.Errorf("list id failed %v", err)
				continue
			}
			for _, groupCode := range groups {
				notify := NewConcernLiveNotify(groupCode, event)
				c.notify <- notify
				if event.Living {
					log.WithFields(localutils.GroupLogFields(groupCode)).Debug("living notify")
				} else {
					log.WithFields(localutils.GroupLogFields(groupCode)).Debug("noliving notify")
				}
			}
		}
	}
}

func (c *Concern) FindRoom(roomId string, load bool) (*LiveInfo, error) {
	var liveInfo *LiveInfo
	if load {
		var err error
		liveInfo, err = RoomPage(roomId)
		if err != nil {
			return nil, err
		}
		_ = c.StateManager.AddLiveInfo(liveInfo)
	}
	if liveInfo != nil {
		return liveInfo, nil
	}
	return c.StateManager.GetLiveInfo(roomId)
}

func (c *Concern) FindOrLoadRoom(roomId string) (*LiveInfo, error) {
	info, _ := c.FindRoom(roomId, false)
	if info == nil {
		return c.FindRoom(roomId, true)
	}
	return info, nil
}

func NewConcern(notify chan<- concern.Notify) *Concern {
	c := &Concern{
		StateManager: NewStateManager(),
		eventChan:    make(chan concernEvent, 500),
		notify:       notify,
		stop:         make(chan interface{}),
	}
	return c
}
