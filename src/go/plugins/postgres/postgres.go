/*
** Zabbix
** Copyright (C) 2001-2019 Zabbix SIA
**
** This program is free software; you can redistribute it and/or modify
** it under the terms of the GNU General Public License as published by
** the Free Software Foundation; either version 2 of the License, or
** (at your option) any later version.
**
** This program is distributed in the hope that it will be useful,
** but WITHOUT ANY WARRANTY; without even the implied warranty of
** MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
** GNU General Public License for more details.
**
** You should have received a copy of the GNU General Public License
** along with this program; if not, write to the Free Software
** Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.
**/

package postgres

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"zabbix.com/pkg/plugin"
)

const pluginName = "Postgres"

var maxParams = map[string]int{

	keyPostgresPing:                                      3,
	keyPostgresTransactions:                              3,
	keyPostgresConnections:                               3,
	keyPostgresWal:                                       3,
	keyPostgresStat:                                      3,
	keyPostgresStatSum:                                   3,
	keyPostgresReplicationCount:                          3,
	keyPostgresReplicationStatus:                         3,
	keyPostgresReplicationLagSec:                         3,
	keyPostgresReplicationRecoveryRole:                   3,
	keyPostgresReplicationLagB:                           3,
	keyPostgresReplicationMasterDiscoveryApplicationName: 3,
	keyPostgresLocks:                                     3,
	keyPostgresOldestXid:                                 3,
	keyPostgresUptime:                                    3,
	keyPostgresCache:                                     3,
	keyPostgresSizeArchive:                               3,
	keyPostgresDiscoveryDatabases:                        3,
	keyPostgresDatabasesBloating:                         3,
	keyPostgresDatabasesSize:                             4,
	keyPostgresDatabasesAge:                              4,
	keyPostgresBgwriter:                                  3,
	keyPostgresAutovacuum:                                3,
}

// Plugin inherits plugin.Base and store plugin-specific data.
type Plugin struct {
	plugin.Base
	connMgr *connManager
	options PluginOptions
}

type requestHandler func(conn *postgresConn, key string, params []string) (res interface{}, err error)

// impl is the pointer to the plugin implementation.
var impl Plugin

func (p *Plugin) Start() {
	p.connMgr = p.NewConnManager(
		time.Duration(p.options.KeepAlive)*time.Second,
		time.Duration(p.options.Timeout)*time.Second,
	)
}

func (p *Plugin) Stop() {
	p.connMgr.stop()
	p.connMgr = nil
}

// Export implements the Exporter interface.
func (p *Plugin) Export(key string, params []string, ctx plugin.ContextProvider) (result interface{}, err error) {
	var (
		connString string
		handler    requestHandler
		session    *Session
	)
	if len(params) > 0 && len(params[0]) > 0 {
		var ok bool
		if session, ok = p.options.Sessions[params[0]]; !ok {
			u, err := url.Parse(params[0])
			if err != nil {
				return nil, fmt.Errorf("Invalid connection URI: %s", err)
			}
			if u.Host == "" {
				u.Host = p.options.Host
			}
			session = &Session{
				Host:     u.Host,
				Database: u.Path,
			}
			var port uint64
			if u.Port() != "" {
				if port, err = strconv.ParseUint(u.Port(), 10, 16); err != nil {
					return nil, fmt.Errorf("Invalid connection port: %s", err)
				}
				session.Port = uint16(port)
			} else {
				session.Port = p.options.Port
			}
			if len(params) > 1 {
				session.User = params[1]
			} else {
				session.User = p.options.User
			}
			if len(params) > 2 {
				session.Password = params[2]
			} else {
				session.Password = p.options.Password
			}

		}
	} else {
		if len(params) > 1 && params[1] == "" {
			return nil, errors.New("Invalid second parameter.")
		}
		if len(params) > 2 && params[2] == "" {
			return nil, errors.New("Invalid third parameter.")
		}
		session = &Session{
			Host:     p.options.Host,
			Port:     p.options.Port,
			Database: p.options.Database,
			User:     p.options.User,
			Password: p.options.Password,
		}
	}

	u := url.URL{
		Scheme: "postgresql",
		Host:   session.Host,
		Path:   session.Database,
		User:   url.UserPassword(session.User, session.Password),
	}
	if session.Port != 0 {
		u.Host += fmt.Sprintf(":%d", session.Port)
	}
	connString = u.String()

	switch key {
	case keyPostgresDiscoveryDatabases:
		handler = p.databasesDiscoveryHandler // postgres.databasesdiscovery[[connString][,section]]

	case keyPostgresDatabasesBloating:
		handler = p.databasesBloatingHandler // postgres.databases[[connString][,section]]

	case keyPostgresDatabasesSize:
		handler = p.databasesSizeHandler // postgres.databases[[connString][,section]]

	case keyPostgresDatabasesAge:
		handler = p.databasesAgeHandler // postgres.databases[[connString][,section]]

	case keyPostgresTransactions:
		handler = p.transactionsHandler // postgres.transactions[[connString]]

	case keyPostgresSizeArchive:
		handler = p.archiveHandler // postgres.archive[[connString]]

	case keyPostgresPing:
		handler = p.pingHandler // postgres.ping[[connString]]

	case keyPostgresConnections:
		handler = p.connectionsHandler // postgres.connections[[connString]]

	case keyPostgresWal:
		handler = p.walHandler // postgres.wal[[connString]]

	case keyPostgresAutovacuum:
		handler = p.autovacuumHandler // postgres.autovacuum.count[[connString]]

	case keyPostgresStat,
		keyPostgresStatSum:
		handler = p.dbStatHandler // postgres.stat[[connString][,section]]

	case keyPostgresBgwriter:
		handler = p.bgwriterHandler // postgres.bgwriter[[connString]]

	case keyPostgresUptime:
		handler = p.uptimeHandler // postgres.uptime[[connString]]

	case keyPostgresCache:
		handler = p.cacheHandler // postgres.cache[[connString]]

	case keyPostgresReplicationCount,
		keyPostgresReplicationStatus,
		keyPostgresReplicationLagSec,
		keyPostgresReplicationRecoveryRole,
		keyPostgresReplicationLagB,
		keyPostgresReplicationMasterDiscoveryApplicationName:
		handler = p.replicationHandler // postgres.replication[[connString][,section]]

	case keyPostgresLocks:
		handler = p.locksHandler // postgres.locks[[connString]]

	case keyPostgresOldestXid:
		handler = p.oldestHandler // postgres.locks[[connString]

	default:
		return nil, errorUnsupportedMetric
	}

	if len(params) > maxParams[key] {
		return nil, errorTooManyParameters
	}

	conn, err := p.connMgr.GetPostgresConnection(connString)
	if err != nil {
		fmt.Println("cannot connect to PG ")
		// Here is another logic of processing connection errors if postgres.ping is requested
		if key == keyPostgresPing {
			return postgresPingFailed, nil
		}
		p.Errf("connection error: %s", err)
		p.Debugf("parameters: %+v", params)
		return nil, errors.New(formatZabbixError(err.Error()))
	}

	var handlerParams []string
	if len(params) > 0 {
		path := strings.TrimLeft(u.Path, "/")
		handlerParams = []string{path}
	} else {
		handlerParams = make([]string, 0)
	}
	return handler(conn, key, handlerParams)
}

// init registers metrics.
func init() {
	plugin.RegisterMetrics(&impl, pluginName,
		keyPostgresPing, "Test if connection is alive or not.",
		keyPostgresTransactions, "Returns JSON for active,idle, waiting and prepared transactions.",
		keyPostgresConnections, "Returns JSON for sum of each type of connection.",
		keyPostgresWal, "Returns JSON wal by type.",
		keyPostgresStat, "Returns JSON for sum of each type of statistic.",
		keyPostgresBgwriter, "Returns JSON for sum of each type of bgwriter statistic.",
		keyPostgresUptime, "Returns uptime.",
		keyPostgresCache, "Returns cache hit percent.",
		keyPostgresSizeArchive, "Returns info about size of archive files.",
		keyPostgresDiscoveryDatabases, "Returns JSON discovery rule with names of databases.",
		keyPostgresDatabasesBloating, "Returns percent of bloating tables for each database.",
		keyPostgresDatabasesSize, "Returns size for each database.",
		keyPostgresDatabasesAge, "Returns age for each database.",
		keyPostgresStatSum, "Returns JSON for sum of each type of statistic for all database.",
		keyPostgresReplicationCount, "Returns number of standby servers.",
		keyPostgresReplicationStatus, "Returns postgreSQL replication status.",
		keyPostgresReplicationLagSec, "Returns replication lag with Master in seconds.",
		keyPostgresReplicationLagB, "Returns replication lag with Master in byte.",
		keyPostgresReplicationRecoveryRole, "Returns postgreSQL recovery role.",
		keyPostgresLocks, "Returns collect all metrics from pg_locks.",
		keyPostgresOldestXid, "Returns age of oldest xid.",
		keyPostgresAutovacuum, "Returns count of autovacuum workers.",
		keyPostgresReplicationMasterDiscoveryApplicationName, "Returns JSON discovery with application name from pg_stat_replication.",
	)
	/* registerConnectionsMertics() */
}
