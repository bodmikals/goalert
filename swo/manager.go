package swo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/stdlib"
	"github.com/target/goalert/app/lifecycle"
	"github.com/target/goalert/swo/swomsg"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Manager struct {
	id uuid.UUID

	dbOld, dbNew *sql.DB

	protectedDB *sql.DB

	s Syncer

	app lifecycle.PauseResumer

	stats *StatsManager

	msgLog     *swomsg.Log
	nextMsgLog *swomsg.Log

	msgCh     chan *swomsg.Message
	nextMsgCh chan *swomsg.Message
	errCh     chan error

	msgState *state

	cancel func()

	canExec bool
}

type Node struct {
	ID uuid.UUID

	OldValid bool
	NewValid bool
	CanExec  bool

	Status string
}

type Config struct {
	OldDBC, NewDBC driver.Connector
	CanExec        bool
}

func NewManager(cfg Config) (*Manager, error) {
	gCfg := &gorm.Config{PrepareStmt: true}
	gormOld, err := gorm.Open(postgres.New(postgres.Config{Conn: sql.OpenDB(cfg.OldDBC)}), gCfg)
	if err != nil {
		return nil, fmt.Errorf("open old database: %w", err)
	}
	gormNew, err := gorm.Open(postgres.New(postgres.Config{Conn: sql.OpenDB(cfg.NewDBC)}), gCfg)
	if err != nil {
		return nil, fmt.Errorf("open new database: %w", err)
	}

	id := uuid.New()
	msgLog, err := swomsg.NewLog(gormOld, id)
	if err != nil {
		return nil, fmt.Errorf("create old message log: %w", err)
	}

	msgLogNext, err := swomsg.NewLog(gormNew, id)
	if err != nil {
		return nil, fmt.Errorf("create new message log: %w", err)
	}

	sm := NewStatsManager()
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		dbOld: sql.OpenDB(cfg.OldDBC),
		dbNew: sql.OpenDB(cfg.NewDBC),

		protectedDB: sql.OpenDB(NewConnector(cfg.OldDBC, cfg.NewDBC, sm)),

		id:         id,
		msgLog:     msgLog,
		nextMsgLog: msgLogNext,
		canExec:    cfg.CanExec,
		msgCh:      make(chan *swomsg.Message),
		nextMsgCh:  make(chan *swomsg.Message),
		errCh:      make(chan error, 10),
		cancel:     cancel,

		stats: sm,
	}

	m.msgState, err = newState(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("create state: %w", err)
	}

	go func() {
		for {
			msg, err := m.msgLog.Next(ctx)
			if err != nil {
				m.errCh <- fmt.Errorf("read from log: %w", err)
				return
			}
			err = m.msgState.processFromOld(ctx, msg)
			if err != nil {
				m.errCh <- fmt.Errorf("process from old db log: %w", err)
				return
			}
		}
	}()
	go func() {
		msg, err := m.nextMsgLog.Next(ctx)
		if err != nil {
			m.errCh <- fmt.Errorf("read from next log: %w", err)
			return
		}
		err = m.msgState.processFromNew(ctx, msg)
		if err != nil {
			m.errCh <- fmt.Errorf("process from new db log: %w", err)
			return
		}
	}()

	return m, nil
}

func (m *Manager) SetPauseResumer(app lifecycle.PauseResumer) { m.app = app }

// withConnFromOld allows performing operations with a raw connection to the old database.
func (m *Manager) withConnFromOld(ctx context.Context, f func(context.Context, *pgx.Conn) error) error {
	return WithLockedConn(ctx, m.dbOld, f)
}

// withConnFromNew allows performing operations with a raw connection to the new database.
func (m *Manager) withConnFromNew(ctx context.Context, f func(context.Context, *pgx.Conn) error) error {
	return WithLockedConn(ctx, m.dbNew, f)
}

// withConnFromBoth allows performing operations with a raw connection to both databases database.
func (m *Manager) withConnFromBoth(ctx context.Context, f func(ctx context.Context, oldConn, newConn *pgx.Conn) error) error {
	// grab lock with old DB first
	return WithLockedConn(ctx, m.dbOld, func(ctx context.Context, oldConn *pgx.Conn) error {
		return WithLockedConn(ctx, m.dbNew, func(ctx context.Context, newConn *pgx.Conn) error {
			return f(ctx, oldConn, newConn)
		})
	})
}

func WithLockedConn(ctx context.Context, db *sql.DB, runFunc func(context.Context, *pgx.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.Raw(func(driverConn interface{}) error {
		conn := driverConn.(*stdlib.Conn).Conn()
		err := SwitchOverExecLock(ctx, conn)
		if err != nil {
			return err
		}

		return runFunc(ctx, conn)
	})
}

func (m *Manager) Status() *Status { return m.msgState.Status() }
func (m *Manager) DB() *sql.DB     { return m.protectedDB }

type Status struct {
	Details string
	Nodes   []Node

	// IsDone is true if the switch has already been completed.
	IsDone bool

	// IsIdle must be true before executing a switch-over.
	IsIdle bool
}