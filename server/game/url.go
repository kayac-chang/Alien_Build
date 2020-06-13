package game

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/YWJSonic/ServerUtility/code"
	"github.com/YWJSonic/ServerUtility/foundation"
	"github.com/YWJSonic/ServerUtility/httprouter"
	"github.com/YWJSonic/ServerUtility/igame"
	"github.com/YWJSonic/ServerUtility/messagehandle"
	"github.com/YWJSonic/ServerUtility/socket"
	"github.com/gorilla/websocket"
	"gitlab.fbk168.com/gamedevjp/alien/server/game/constants"
	"gitlab.fbk168.com/gamedevjp/alien/server/game/db"
	"gitlab.fbk168.com/gamedevjp/alien/server/game/gamerule"
	"gitlab.fbk168.com/gamedevjp/alien/server/game/protoc"
)

func (g *Game) createNewSocket(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")

	if err := g.CheckToken(token); err != nil {
		log.Fatal("createNewSocket: not this token\n")
		return
	}

	c, err := g.Server.Socket.Upgrade(w, r, r.Header)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	g.Server.Socket.AddNewConn("f", c, func(msg socket.Message) error {
		fmt.Println("#-- socket --#", msg)
		return nil
	})
	// g.Server.Socket.AddNewConn(user.GetGameInfo().GameAccount, c, g.SocketMessageHandle)

	time.Sleep(time.Second * 3)
	g.Server.Socket.ConnMap["f"].Send(websocket.CloseMessage, []byte{})
}

// SocketMessageHandle ...
func (g *Game) SocketMessageHandle(msg socket.Message) error {
	fmt.Println("#-- socket --#", msg)
	return nil
}

func (g *Game) gameinit(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var proto protoc.InitRequest
	proto.InitData(r)

	// get user
	user, errproto, err := g.GetUser(proto.Token)
	if errproto != nil {
		errMsg := messagehandle.New()
		errMsg.ErrorCode = code.GetUserError
		errMsg.Msg = fmt.Sprintf("%d : %s:", errproto.GetCode(), errproto.GetMessage())
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}
	if err != nil {
		errMsg := messagehandle.New()
		errMsg.ErrorCode = code.GetUserError
		errMsg.Msg = err.Error()
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}

	user.LoadAttach()
	result := map[string]interface{}{
		"attach":  g.IGameRule.(*gamerule.Rule).GetGameAttach(user),
		"betrate": g.IGameRule.GetBetSetting(),
		"player": map[string]interface{}{
			"gametypeid": g.IGameRule.GetGameTypeID(),
			"id":         user.UserGameInfo.IDStr,
			"money":      user.UserGameInfo.GetMoney(),
		},
		"reel": g.IGameRule.GetReel(),
	}

	// if gameRule.IsAttachInit {
	// 	result["attach"] = gameRule.ConvertToGameAttach(playerInfo.ID, attach.GetAttach(playerInfo.ID, gameRule.GameIndex, gameRule.IsAttachSaveToDB))
	// }
	g.Server.HTTPResponse(w, result, messagehandle.New())
}

func (g *Game) gameresult(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var proto protoc.GameRequest
	var oldMoney int64
	proto.InitData(r)

	if proto.GameTypeID != g.IGameRule.GetGameTypeID() {
		errMsg := messagehandle.New()
		errMsg.ErrorCode = code.GameTypeError
		errMsg.Msg = "GameTypeError"
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}

	user, errproto, err := g.GetUser(proto.Token)
	if errproto != nil {
		errMsg := messagehandle.New()
		errMsg.ErrorCode = code.NewOrderError
		errMsg.Msg = fmt.Sprintf("%d : %s:", errproto.GetCode(), errproto.GetMessage())
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}
	if err != nil {
		errMsg := messagehandle.New()
		errMsg.ErrorCode = code.GetUserError
		errMsg.Msg = err.Error()
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}

	if user.UserGameInfo.GetMoney() < g.IGameRule.GetBetMoney(proto.BetIndex) {
		err := messagehandle.New()
		err.Msg = "NoMoneyToBet"
		err.ErrorCode = code.NoMoneyToBet
		g.Server.HTTPResponse(w, "", err)
		return
	}

	order, errproto, err := g.NewOrder(proto.Token, user.UserGameInfo.IDStr, g.IGameRule.GetBetMoney(proto.BetIndex))
	if errproto != nil {
		errMsg := messagehandle.New()
		errMsg.Msg = fmt.Sprintf("%d : %s:", errproto.GetCode(), errproto.GetMessage())
		errMsg.ErrorCode = code.NewOrderError
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}
	if err != nil {
		errMsg := messagehandle.New()
		errMsg.Msg = err.Error()
		errMsg.ErrorCode = code.NewOrderError
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}

	// get attach
	user.LoadAttach()
	// get game result
	RuleRequest := &igame.RuleRequest{
		BetIndex: proto.BetIndex,
		Attach:   &user.IAttach,
	}

	result := g.IGameRule.GameRequest(RuleRequest)
	for _, att := range result.Attach {
		// user.IAttach.SetValue(att.GetKind(), att.GetTypes(), att.GetSValue(), att.GetIValue())
		user.IAttach.SetAttach(att)
	}

	user.IAttach.Save()
	user.UserGameInfo.SumMoney(result.Totalwinscore - result.BetMoney)

	resultMap := make(map[string]interface{})
	resultMap["totalwinscore"] = result.Totalwinscore
	resultMap["playermoney"] = user.UserGameInfo.GetMoney()
	resultMap["normalresult"] = result.GameResult["normalresult"]
	resultMap["attach"] = map[string]interface{}{
		"PlayerID":            user.UserGameInfo.ID,
		"Kind":                7,
		"JackPartBonusPoolx2": result.OtherData["JackPartBonusPoolx2"],
		"JackPartBonusPoolx3": result.OtherData["JackPartBonusPoolx3"],
		"JackPartBonusPoolx5": result.OtherData["JackPartBonusPoolx5"],
	}

	respin, ok := result.OtherData["isrespin"]
	resultMap["isrespin"] = 0
	if ok && respin == 1 {
		resultMap["isrespin"] = 1
		resultMap["respin"] = result.GameResult["respin"]
	}

	msg := foundation.JSONToString(resultMap)
	msg = strings.ReplaceAll(msg, "\"", "\\\"")
	errMsg := db.SetLog(
		g.Server.DBConn("logdb"),
		user.UserGameInfo.IDStr,
		0,
		time.Now().Unix(),
		constants.ActionGameResult,
		oldMoney,
		user.UserGameInfo.GetMoney(),
		result.Totalwinscore,
		"",
		"",
		"",
		msg,
	)
	if errMsg.ErrorCode != code.OK {
		fmt.Println(resultMap, errMsg)
	}

	_, errproto, err = g.EndOrder(proto.Token, order)
	if errproto != nil {
		errMsg := messagehandle.New()
		errMsg.Msg = fmt.Sprintf("%d : %s:", errproto.GetCode(), errproto.GetMessage())
		errMsg.ErrorCode = code.NewOrderError
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}
	if err != nil {
		errMsg := messagehandle.New()
		errMsg.Msg = err.Error()
		errMsg.ErrorCode = code.NewOrderError
		g.Server.HTTPResponse(w, "", errMsg)
		return
	}
	g.Server.HTTPResponse(w, resultMap, messagehandle.New())
}
