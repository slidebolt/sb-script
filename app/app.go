package app

import (
	"encoding/json"
	"fmt"
	"log"

	contract "github.com/slidebolt/sb-contract"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	server "github.com/slidebolt/sb-script/server"
	storage "github.com/slidebolt/sb-storage-sdk"
)

const ServiceID = "sb-script"

type App struct {
	svc *server.Service
	msg messenger.Messenger
	sto storage.Storage
}

func New() *App {
	return &App{}
}

func (a *App) Hello() contract.HelloResponse {
	return contract.HelloResponse{
		ID:              ServiceID,
		Kind:            contract.KindPlugin,
		ContractVersion: contract.ContractVersion,
		DependsOn:       []string{"messenger", "storage"},
	}
}

func (a *App) OnStart(deps map[string]json.RawMessage) (json.RawMessage, error) {
	msg, err := messenger.Connect(deps)
	if err != nil {
		return nil, fmt.Errorf("connect messenger: %w", err)
	}
	a.msg = msg

	store, err := storage.Connect(deps)
	if err != nil {
		return nil, fmt.Errorf("connect storage: %w", err)
	}
	a.sto = store

	svc, err := server.New(msg, store)
	if err != nil {
		return nil, err
	}
	a.svc = svc

	log.Println("sb-script: started")
	return nil, nil
}

func (a *App) OnShutdown() error {
	if a.svc != nil {
		a.svc.Shutdown()
	}
	if a.sto != nil {
		a.sto.Close()
	}
	if a.msg != nil {
		a.msg.Close()
	}
	return nil
}
