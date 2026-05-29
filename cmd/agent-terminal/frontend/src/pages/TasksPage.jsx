import React from 'react';
import { logDebug } from '../services/log.js';
import { CronPanel } from './CronPanel.jsx';

export function TasksPage({ tasksSubTab = 'acks', items = [], fields = [], onSubTabChange }) {
    function setSubTab(tab) {
        logDebug('page', 'tasks.subTab.changed', { tab });
        onSubTabChange?.(tab);
    }

    return (
        <section id="page-tasks" className="page active" data-testid="tasks-page">
            <div className="panel-header">
                <div className="ph-bar"></div>
                <div className="ph-text"><h2>任务管理</h2></div>
            </div>
            <div className="sub-tabs" data-testid="tasks-sub-tabs">
                <button
                    className={`sub-tab ${tasksSubTab === 'acks' ? 'active' : ''}`}
                    data-testid="tasks-subtab-acks"
                    onClick={() => setSubTab('acks')}
                >
                    任务工单
                </button>
                <button
                    className={`sub-tab ${tasksSubTab === 'traces' ? 'active' : ''}`}
                    data-testid="tasks-subtab-traces"
                    onClick={() => setSubTab('traces')}
                >
                    执行追踪
                </button>
                <button
                    className={`sub-tab ${tasksSubTab === 'cron' ? 'active' : ''}`}
                    data-testid="tasks-subtab-cron"
                    onClick={() => setSubTab('cron')}
                >
                    定时任务
                </button>
            </div>
            <div className="panel-body" data-testid="tasks-panel-body">
                {tasksSubTab === 'cron' ? (
                    <CronPanel />
                ) : (
                    <>
                        {items.length === 0 ? (
                            <div className="empty-state" data-testid="tasks-empty-state">
                                <div className="es-icon">T</div>
                                <h3>暂无任务</h3>
                            </div>
                        ) : (
                            <div className="data-list-vue" data-testid="tasks-list">
                                {items.map((item, idx) => (
                                    <article
                                        key={item.ack_key || item.trace_id || idx}
                                        className="data-card-vue"
                                        data-testid={`tasks-card-${idx}`}
                                    >
                                        {fields.map((field) => (
                                            <div key={field.key} className="data-row-vue">
                                                <strong>{field.label}</strong>
                                                <span>{item[field.key] ?? '-'}</span>
                                            </div>
                                        ))}
                                    </article>
                                ))}
                            </div>
                        )}
                    </>
                )}
            </div>
        </section>
    );
}
