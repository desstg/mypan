<template>
  <div class="automation-page">
    <template v-if="viewMode === 'list'">
      <section class="admin-panel-table-wrap automation-list-panel">
        <div class="panel-head">
          <div>
            <div class="panel-title">自动联动</div>
            <div class="panel-sub">示例：定时执行整理任务，质量达标后联动 STRM 和 Emby 刷库。</div>
          </div>
          <div class="panel-head-actions">
            <AppButton type="button" size="sm" variant="secondary" @click="openRuns">
              <i class="fas fa-clock-rotate-left"></i>
              运行记录
            </AppButton>
            <AppBadge tone="info">{{ rules.length }} 条规则</AppBadge>
          </div>
        </div>
        <div class="table-wrap">
          <table class="admin-table automation-table">
            <thead>
              <tr>
                <th class="col-name">联动名称</th>
                <th class="col-flow">执行流程</th>
                <th class="col-last">上次结果</th>
                <th class="col-next">下次执行</th>
                <th class="col-op">操作</th>
              </tr>
            </thead>
            <tbody ref="automationRuleList">
              <tr v-if="loading">
                <td colspan="5" class="empty-cell">加载中...</td>
              </tr>
              <tr v-else-if="rules.length === 0">
                <td colspan="5" class="empty-cell">还没有自动联动，点右上角「新增联动」创建第一条规则</td>
              </tr>
              <tr v-for="rule in rules" v-else :key="rule.id" class="automation-row" :class="{ 'is-running': rule.is_running }" :data-dust-key="`automation-rule-${rule.id}`">
                <td>
                  <div class="rule-name">{{ rule.name }}</div>
                  <div class="rule-desc">{{ triggerLabel(rule) }}</div>
                </td>
                <td class="flow-cell flow-hover-zone">
                  <div
                    class="flowtext"
                    :ref="el => setFlowViewportRef(rule.id, el)"
                  >
                    <div class="flow-track">
                      <template v-for="(action, index) in rule.actions" :key="action.id || index">
                        <span v-if="index > 0" class="arrow">→</span>
                        <span class="seg" :class="{ running: isRuleActionRunning(rule, index) }">
                          <i :class="actionIcon(action.type)"></i>{{ actionLabel(action) }}
                        </span>
                      </template>
                    </div>
                  </div>
                  <div v-if="!rule.is_running" class="flowtext-wide">
                    <template v-for="(action, index) in rule.actions" :key="`wide-${action.id || index}`">
                      <span v-if="index > 0" class="arrow">→</span>
                      <span class="seg">
                        <i :class="actionIcon(action.type)"></i>{{ actionLabel(action) }}
                      </span>
                    </template>
                  </div>
                </td>
                <td class="borrowable-cell status-cell">
                  <AdminRunStatusCell
                    :title="rule.last_run_message || lastStatusLabel(rule)"
                    :primary="lastStatusLabel(rule)"
                    :summary="rule.last_run_message || formatDate(rule.last_run_at)"
                    :variant="lastStatusVariant(rule)"
                    :live="rule.is_running"
                    primary-tone="strong"
                  />
                </td>
                <td class="next-cell borrowable-cell">{{ formatDate(rule.next_run_at) }}</td>
                <td class="admin-table__actions">
                  <AdminRowActions>
                    <AdminEnableToggle
                      :enabled="rule.status === 'running'"
                      aria-label="自动联动启用切换"
                      @enable="enabled => setRuleEnabled(rule, enabled)"
                    />
                    <AdminTableActionBtn icon="play" title="立即执行" :disabled="rule.is_running" @click="runRule(rule)" />
                    <AdminTableActionBtn icon="edit" title="编辑" @click="openBuilder(rule)" />
                    <AdminTableActionBtn icon="delete" title="删除" danger @click="deleteRule(rule)" />
                    <template #menu>
                      <button class="admin-row-actions__item" type="button" @click="setRuleEnabled(rule, rule.status !== 'running')">
                        {{ rule.status === 'running' ? '停用' : '启用' }}
                      </button>
                      <button class="admin-row-actions__item" type="button" :disabled="rule.is_running" @click="runRule(rule)">立即执行</button>
                      <button class="admin-row-actions__item" type="button" @click="openBuilder(rule)">编辑</button>
                      <button class="admin-row-actions__item admin-row-actions__item--danger" type="button" @click="deleteRule(rule)">删除</button>
                    </template>
                  </AdminRowActions>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <Teleport to="body">
        <div v-if="runsDrawerVisible" class="runs-drawer-overlay" @click.self="closeRuns">
          <div class="runs-drawer">
            <div class="runs-drawer-head">
              <div>
                <div class="runs-drawer-title">运行记录</div>
                <div class="runs-drawer-sub">全部联动最近 20 条执行记录</div>
              </div>
              <div class="runs-drawer-actions">
                <button type="button" class="runs-clear-btn" :disabled="runsLoading || runs.length === 0" @click="clearRuns">
                  <i class="fas fa-trash"></i>
                  清空
                </button>
                <button type="button" class="runs-drawer-close" title="关闭" @click="closeRuns">
                  <i class="fas fa-times"></i>
                </button>
              </div>
            </div>
            <div class="runs-drawer-body">
              <div v-if="runsLoading" class="runs-drawer-empty">加载中...</div>
              <div v-else-if="runs.length === 0" class="runs-drawer-empty">暂无运行记录</div>
              <ul v-else class="runs-list">
                <li v-for="run in runs" :key="run.id" class="runs-item" :class="run.status">
                  <button type="button" class="runs-card-head" @click="toggleRunExpanded(run.id)">
                    <span class="runs-item-name">{{ ruleNameById(run.rule_id) }}</span>
                    <span class="runs-status-mini" :class="run.status">{{ runStatusText(run.status) }}</span>
                    <i class="fas fa-chevron-down runs-expand-ico" :class="{ open: isRunExpanded(run.id) }"></i>
                    <span class="runs-item-meta-line">{{ runSourceLabel(run.trigger_source) }} · {{ formatDate(run.started_at) }}</span>
                  </button>
                  <ul v-if="isRunExpanded(run.id) && runStepItems(run).length" class="runs-steps">
                    <li v-for="step in runStepItems(run)" :key="step.index" class="runs-step" :class="step.status">
                      <span class="runs-step-dot" :class="step.status"></span>
                      <div class="runs-step-body">
                        <div class="runs-step-title">
                          <strong>{{ step.name || '未知动作' }}</strong>
                          <span>第 {{ Number(step.index || 0) + 1 }} 步</span>
                        </div>
                        <div class="runs-step-msg">{{ step.message || stepStatusText(step.status) }}</div>
                      </div>
                    </li>
                  </ul>
                </li>
              </ul>
            </div>
          </div>
        </div>
      </Teleport>
    </template>

    <template v-else>
      <div class="builder-grid">
        <div class="builder-main">
          <div class="sec-title">
            当
            <span class="hint">满足触发条件时启动</span>
          </div>
          <div v-if="form.trigger_type" class="node choice-node" @click="openTriggerPicker">
            <div class="node-main compact">
              <div class="node-ico trigger"><i class="fas fa-clock"></i></div>
              <div class="node-body">
                <div class="node-title">{{ triggerNodeTitle }}</div>
                <div class="node-sub">{{ triggerNodeSub }}</div>
              </div>
              <span class="node-chev"><i class="fas fa-chevron-right"></i></span>
            </div>
          </div>
          <div v-else class="node add-node" @click="openTriggerPicker">
            <div class="node-main">
              <div class="node-ico trigger"><i class="fas fa-clock"></i></div>
              <div class="node-body">
                <div class="node-title ph">添加触发条件</div>
                <div class="node-sub">时间 / 间隔 / 第三方通知</div>
              </div>
            </div>
          </div>

          <div class="sec-title after-title">
            就执行
            <span class="hint">某个任务</span>
          </div>

          <div class="flow-list">
            <div
              v-if="form.actions[0]"
              class="node action-node primary-action"
              data-action-index="0"
            >
              <div class="node-main compact" @click="openActionConfigFromCard(0)">
                <div class="node-ico act" :class="form.actions[0].type">
                  <i :class="actionIcon(form.actions[0].type)"></i>
                </div>
                <div class="node-body">
                  <div class="node-title">{{ actionNodeTitle(form.actions[0]) }}</div>
                  <div class="node-sub">{{ actionNodeSub(form.actions[0]) }}</div>
                </div>
                <button class="node-del" type="button" title="移除" draggable="false" @dragstart.stop.prevent @click.stop="removeAction(0)">
                  <i class="fas fa-times"></i>
                </button>
              </div>
            </div>
            <div v-else class="node add-node" @click="openActionPicker(0)">
              <div class="node-main">
                <div class="node-ico add"><i class="fas fa-plus"></i></div>
                <div class="node-body">
                  <div class="node-title ph">选择要执行的任务</div>
                  <div class="node-sub">整理 / STRM / 延迟 / Emby 全局刷库</div>
                </div>
              </div>
            </div>

            <div class="sec-title linked-title">
              <div>
                联动执行
                <span class="hint">可选，可添加多个执行任务</span>
              </div>
              <button
                v-if="linkedActionItems.length > 1"
                class="sort-mode-btn"
                type="button"
                :class="{ active: linkedSortMode }"
                @click="toggleLinkedSortMode"
              >
                <i class="fas" :class="linkedSortMode ? 'fa-check' : 'fa-up-down-left-right'"></i>
                {{ linkedSortMode ? '完成排序' : '排序' }}
              </button>
            </div>

            <div
              v-if="linkedSortMode && draggingActionIndex !== null && linkedActionItems.length > 0"
              class="action-drop-zone"
              data-action-drop-index="1"
              :class="{ active: isActiveDropIndex(1) }"
            >
              <span>放到这里</span>
            </div>

            <template v-for="item in visibleLinkedActionItems" :key="item.action.id">
              <div
                class="node action-node"
                :class="{ sortable: linkedSortMode }"
                :data-action-index="item.index"
                @pointerdown="startActionPointerDrag(item.index, $event)"
              >
                <div class="node-main compact" @click="openActionConfigFromCard(item.index)">
                  <div class="node-ico act" :class="item.action.type">
                    <i :class="actionIcon(item.action.type)"></i>
                  </div>
                  <div class="node-body">
                    <div class="node-title">{{ actionNodeTitle(item.action) }}</div>
                    <div class="node-sub">{{ actionNodeSub(item.action) }}</div>
                  </div>
                  <button class="node-del" type="button" title="移除" draggable="false" @dragstart.stop.prevent @click.stop="removeAction(item.index)">
                    <i class="fas fa-times"></i>
                  </button>
                </div>
              </div>
              <div
                v-if="linkedSortMode && draggingActionIndex !== null"
                class="action-drop-zone"
                :data-action-drop-index="item.index + 1"
                :class="{ active: isActiveDropIndex(item.index + 1) }"
              >
                <span>放到这里</span>
              </div>
            </template>

            <div class="node add-node" @click="openActionPicker(form.actions.length)">
              <div class="node-main">
                <div class="node-ico add"><i class="fas fa-plus"></i></div>
                <div class="node-body">
                  <div class="node-title ph">添加联动动作</div>
                  <div class="node-sub">这一步可以不添加，需要时再串联后续任务</div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div
          v-if="dragGhost.visible"
          class="action-drag-ghost"
          :style="dragGhostStyle"
        >
          <div class="node-ico act" :class="dragGhost.type">
            <i :class="actionIcon(dragGhost.type)"></i>
          </div>
          <div class="node-body">
            <div class="node-title">{{ dragGhost.title }}</div>
            <div class="node-sub">{{ dragGhost.sub }}</div>
          </div>
        </div>

        <aside class="rail">
          <div class="card flow-card" :class="{ pass: validationOk, fail: hasValidationError }">
            <div class="flow-card-head">
              <div class="ch">流程预览</div>
              <button class="rail-back" type="button" @click="backToList">
                <i class="fas fa-arrow-left"></i>
                返回列表
              </button>
            </div>
            <div class="cb">
              <div v-if="flowPreviewItems.length === 0" class="panel-sub">配置触发条件和执行动作后，这里会实时显示完整流程。</div>
              <div v-else class="flow-preview">
                <div
                  v-for="(item, index) in flowPreviewItems"
                  :key="`${item.type}-${index}`"
                  class="flow-preview-item"
                  :class="{ trigger: item.type === 'trigger', error: item.issue }"
                >
                  <div class="flow-index">{{ item.order }}</div>
                  <div class="flow-copy">
                    <div class="flow-title">
                      <span>{{ item.title }}</span>
                      <span v-if="item.issue" class="flow-error-icon"><i class="fas fa-times"></i></span>
                    </div>
                    <div v-if="item.issue" class="flow-sub flow-error-text">{{ item.issue.message }}</div>
                    <div v-else-if="item.sub" class="flow-sub">{{ item.sub }}</div>
                  </div>
                </div>
                <div v-if="validationOk" class="flow-ok-text">
                  <i class="fas fa-check-circle"></i>
                  当前流程可以保存
                </div>
              </div>
            </div>
          </div>
        </aside>
      </div>

      <div class="builder-save-footer">
        <div class="save-combo">
          <input v-model="form.name" class="save-name-input" type="text" placeholder="填写联动名称并保存">
          <AppButton class="save-wide" type="button" variant="primary" :disabled="saving || !canSave" @click="saveRule">
            <i class="fas fa-save"></i>
            {{ saving ? '保存中...' : '保存' }}
          </AppButton>
        </div>
      </div>
    </template>

    <TimeWheelPicker
      :visible="timePickerVisible"
      :start-time="timePickerValue"
      :end-time="timePickerValue"
      :allow-daily="true"
      :daily-only="true"
      mode="daily"
      @confirm="confirmTimePicker"
      @cancel="cancelTimePicker"
    />

    <AppPlainModal
      :open="pickerVisible"
      :title="pickerKind === 'trigger' ? '选择触发条件' : '添加执行动作'"
      size="sm"
      body-flush
      @close="cancelPicker"
    >
      <div class="automation-scope">
        <div v-if="pickerKind === 'trigger'" class="pick-list">
          <button class="pick-option" type="button" @click="chooseTrigger('daily')">
            <span class="pick-ico trigger"><i class="fas fa-clock"></i></span>
            <span>
              <b>每天定时</b>
              <em>每天到设定时间触发</em>
            </span>
            <i class="fas fa-chevron-right"></i>
          </button>
          <button class="pick-option" type="button" @click="chooseTrigger('interval')">
            <span class="pick-ico interval"><i class="fas fa-rotate"></i></span>
            <span>
              <b>本次触发时间 + 间隔</b>
              <em>从某个时间开始按间隔循环执行</em>
            </span>
            <i class="fas fa-chevron-right"></i>
          </button>
          <button class="pick-option" type="button" @click="chooseTrigger('external_event')">
            <span class="pick-ico external_event"><i class="fas fa-plug"></i></span>
            <span>
              <b>第三方通知</b>
              <em>外部程序调用 Webhook 接口通知 MyPan</em>
            </span>
            <i class="fas fa-chevron-right"></i>
          </button>
          <button class="pick-option" type="button" @click="chooseTrigger('offline_download')">
            <span class="pick-ico offline_download"><i class="fas fa-cloud-arrow-down"></i></span>
            <span>
              <b>离线下载完成</b>
              <em>指定目录或其子目录中的离线任务完成后触发</em>
            </span>
            <i class="fas fa-chevron-right"></i>
          </button>
        </div>
        <div v-else class="pick-list">
          <button v-for="item in actionTypeOptions" :key="item.value" class="pick-option" type="button" @click="chooseAction(item.value)">
            <span class="pick-ico" :class="item.value"><i :class="actionIcon(item.value)"></i></span>
            <span>
              <b>{{ item.label }}</b>
              <em>{{ item.desc }}</em>
            </span>
            <i class="fas fa-chevron-right"></i>
          </button>
        </div>
      </div>
    </AppPlainModal>

    <AppPlainModal :open="configVisible" :title="configTitle" size="sm" body-flush @close="closeConfig">
      <div class="automation-scope">
        <div v-if="configMode === 'trigger'" class="cfg-body">
          <template v-if="form.trigger_type === 'external_event'">
            <div class="cfg-row">
              <label>通知名称</label>
              <input v-model.trim="form.trigger_config.event" class="ctrl" type="text" placeholder="例如：download_completed">
            </div>
            <div class="cfg-row">
              <label>通知来源（可选）</label>
              <input v-model.trim="form.trigger_config.source" class="ctrl" type="text" placeholder="例如：CloudSaver，不填表示任意来源">
            </div>
            <div class="api-example">
              <div class="api-example-title">调用方式</div>
              <div class="api-example-text">把下面内容填到外部程序的 Webhook / HTTP 通知里。</div>
              <code>POST http://你的MyPan地址:5211/api/open/automation/events</code>
              <code>Authorization: Bearer lpk_api_xxx</code>
              <pre>{{ externalEventJsonExample }}</pre>
            </div>
          </template>
          <template v-else-if="form.trigger_type === 'offline_download'">
            <div class="cfg-row">
              <label>监控目录</label>
              <button class="time-btn" type="button" @click="openOfflineFolderPicker">
                <i class="fas fa-folder-tree"></i>
                {{ offlineDownloadDirectoryLabel }}
              </button>
              <div class="field-tip">该账号中，目标为此目录或其任意子目录的离线任务完成后触发。</div>
            </div>
          </template>
          <template v-else>
          <div class="cfg-row">
            <label>{{ form.trigger_type === 'daily' ? '每天触发时间' : '首次触发时间' }}</label>
            <button class="time-btn" type="button" @click="openTimePicker">
              <i class="fas fa-clock"></i>
              {{ triggerTime || '请选择时间' }}
            </button>
          </div>
          <div v-if="form.trigger_type === 'interval'" class="cfg-row">
            <label>间隔小时</label>
            <input v-model.number="form.trigger_config.interval_hours" class="ctrl" type="number" min="1" max="8760">
          </div>
          </template>
        </div>

        <div v-else-if="configAction" class="cfg-body">
          <template v-if="configAction.type === 'organize'">
            <div class="cfg-row">
              <label>整理任务</label>
              <AppSelect v-model="configAction.params.task_id" :options="organizeTaskOptions" placeholder="请选择整理任务" />
            </div>
            <div class="cfg-row">
              <label>允许异常比例</label>
              <div class="input-with-suffix">
                <input v-model.number="configAction.params.max_risk_percent" type="number" class="ctrl" min="0" max="100">
                <span>%</span>
              </div>
              <div class="field-tip">异常比例 =（失败数 + 异常跳过数）/ 需处理项目数；已整理、已是目标名等正常跳过不计入异常。</div>
            </div>
          </template>
          <template v-else-if="configAction.type === 'strm'">
            <div class="cfg-row">
              <label>STRM任务</label>
              <AppSelect v-model="configAction.params.task_id" :options="strmTaskOptions" placeholder="请选择STRM任务" @update:model-value="taskId => onStrmTaskChange(configAction, taskId)" />
            </div>
            <div class="cfg-row">
              <label>执行方式</label>
              <AppSelect v-model="configAction.params.run_mode" :options="getStrmRunModeOptions(configAction)" />
            </div>
          </template>
          <template v-else-if="configAction.type === 'strm_scrape'">
            <div class="cfg-row">
              <label>STRM任务</label>
              <AppSelect v-model="configAction.params.task_id" :options="strmTaskOptions" placeholder="请选择STRM任务" />
            </div>
            <div class="cfg-row">
              <label>写入策略</label>
              <AppSelect v-model="configAction.params.write_mode" :options="strmScrapeWriteModeOptions" />
            </div>
            <div class="cfg-row">
              <label>联动中断条件</label>
              <AppSelect v-model="configAction.params.failure_policy" :options="strmScrapeFailurePolicyOptions" />
            </div>
            <div class="field-tip">仅控制单个影片刮削失败时是否继续；配置错误、任务取消或服务异常仍会中断联动。</div>
          </template>
          <template v-else-if="configAction.type === 'delay'">
            <div class="cfg-row">
              <label>等待秒数</label>
              <input v-model.number="configAction.params.seconds" type="number" class="ctrl" min="1" max="86400">
            </div>
          </template>
          <template v-else-if="isEmbyScopedAction(configAction)">
            <div class="cfg-row">
              <label>Emby配置</label>
              <AppSelect
                v-model="configAction.params.emby_id"
                :options="embyConfigOptions"
                placeholder="请选择 Emby 配置"
                @update:model-value="embyId => onEmbyConfigChange(configAction, embyId)"
              />
            </div>
            <div class="cfg-row">
              <label>执行范围</label>
              <AppSelect v-model="configAction.params.mode" :options="embyRefreshModeOptions" @update:model-value="mode => onEmbyRefreshModeChange(configAction, mode)" />
            </div>
            <div v-if="configAction.params.mode === 'library'" class="cfg-row">
              <label>媒体库</label>
              <AppSelect
                v-model="configAction.params.library_id"
                :options="embyLibraryOptions"
                :disabled="embyLibrariesLoading || !configAction.params.emby_id"
                :placeholder="embyLibrariesLoading ? '正在加载媒体库...' : '请选择媒体库'"
                @update:model-value="libraryId => onEmbyLibraryChange(configAction, libraryId)"
              />
              <div class="field-tip">媒体库列表从 Emby 实时拉取，仅在配置该动作时按需加载。</div>
              <button class="inline-link-btn" type="button" :disabled="embyLibrariesLoading || !configAction.params.emby_id" @click="ensureEmbyLibrariesLoaded(true)">
                {{ embyLibrariesLoading ? '加载中...' : '刷新媒体库列表' }}
              </button>
            </div>
          </template>
        </div>
      </div>

      <template #footer>
        <AppButton type="button" variant="primary" :disabled="!configCanApply" @click="applyConfig">确定</AppButton>
      </template>
    </AppPlainModal>

    <FolderPickerModal
      :open="offlineFolderPickerVisible"
      title="选择离线下载监控目录"
      confirm-text="选择当前目录"
      :selectable-account="true"
      :account-id="offlineFolderPickerAccountId"
      :accounts="accounts"
      :initial-path="form.trigger_config.path"
      @close="offlineFolderPickerVisible = false"
      @resolve="selectOfflineDownloadDirectory"
    />
  </div>
</template>

<script setup>
import {
  computed,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  reactive,
  ref,
  watch
} from 'vue'
import AppButton from '@/components/base/AppButton.vue'
import AppBadge from '@/components/base/AppBadge.vue'
import AppModal from '@/components/base/AppModal.vue'
import AppPlainModal from '@/components/base/AppPlainModal.vue'
import AppSelect from '@/components/base/AppSelect.vue'
import FolderPickerModal from '@/components/file/FolderPickerModal.vue'
import AdminEnableToggle from '@/components/admin/AdminEnableToggle.vue'
import AdminRowActions from '@/components/admin/AdminRowActions.vue'
import AdminRunStatusCell from '@/components/admin/AdminRunStatusCell.vue'
import AdminTableActionBtn from '@/components/admin/AdminTableActionBtn.vue'
import TimeWheelPicker from '../base/TimeWheelPicker.vue'
import { confirm } from '../../composables/useConfirm'
import { findDustTarget, useDustRemoval } from '../../composables/useDustRemoval'
import { toast } from '../../composables/useToast'
import { getApiErrorMessage } from '../../api/client'
import { accountsApi } from '../../api/accounts'
import {
  clearAutomationRuns,
  createAutomationRule,
  deleteAutomationRule,
  fetchAutomationOptions,
  fetchAutomationRules,
  fetchAutomationRuns,
  normalizeAutomationTriggerConfig,
  runAutomationRule,
  serializeAutomationTriggerConfig,
  toggleAutomationRule,
  updateAutomationRule,
  validateAutomationRule
} from '../../api/automation'
import { fetchEmbyLibraries } from '../../api/emby'
import { formatTime } from '../../utils/format'
import '@/styles/admin-table.css'

const viewMode = ref('list')
const loading = ref(false)
const saving = ref(false)
const editingRule = ref(null)
const rules = ref([])
const automationRuleList = ref(null)
const { removeWithDust } = useDustRemoval()
const runs = ref([])
const expandedRunIds = ref(new Set())
const runsDrawerVisible = ref(false)
const runsLoading = ref(false)
const accounts = ref([])
const emptyOptions = () => ({ organize_tasks: [], strm_tasks: [], emby_configs: [] })
const options = ref(emptyOptions())
const embyLibraries = ref([])
const embyLibrariesLoading = ref(false)
const embyLibrariesLoaded = ref(false)
const embyLibrariesConfigID = ref('')
const validationIssues = ref([])
const validationOk = ref(false)
const timePickerVisible = ref(false)
const timePickerValue = ref('03:00')
const offlineFolderPickerVisible = ref(false)
const pickerVisible = ref(false)
const pickerKind = ref('trigger')
const configVisible = ref(false)
const configMode = ref('trigger')
const configActionIndex = ref(-1)
const actionInsertIndex = ref(0)
const pendingConfigAction = ref(null)
const pendingConfigInsertIndex = ref(-1)
const linkedSortMode = ref(false)
const draggingActionIndex = ref(null)
const actionDropIndex = ref(null)
const suppressActionClick = ref(false)
const dragGhost = reactive({
  visible: false,
  type: '',
  title: '',
  sub: '',
  x: 0,
  y: 0,
  width: 0
})
let validationTimer = null
let validationSeq = 0
let rulesRefreshTimer = null
const flowViewportRefs = new Map()
let pendingActionDrag = null
let flowCenterFrame = null
let flowCenterTimer = null

const form = reactive({
  name: '',
  trigger_type: '',
  trigger_config: normalizeAutomationTriggerConfig(),
  status: 'running',
  actions: []
})

const ACTION_DEFINITIONS = {
  cache_clear: {
    label: '刷新目录',
    optionLabel: '刷新目录',
    icon: 'fas fa-broom',
    desc: '清理后续任务涉及账号的全部目录缓存',
    normalize: () => ({}),
    canApply: () => true,
    nodeTitle: () => '刷新目录',
    previewTitle: () => '刷新目录'
  },
  organize: {
    label: '整理任务',
    optionLabel: '执行整理任务',
    icon: 'fas fa-folder-tree',
    desc: '生成计划并执行整理，结果会经过质量门槛判断',
    normalize: params => ({
      task_id: params.task_id ? String(params.task_id) : '',
      max_risk_percent: Number(params.max_risk_percent ?? 30)
    }),
    canApply: action => Boolean(String(action.params.task_id || '').trim()),
    nodeTitle: action => `整理「${findTaskLabel('organize', action.params.task_id)}」`,
    previewTitle: action => `整理任务[${findTaskLabel('organize', action.params.task_id)}]`
  },
  strm: {
    label: 'STRM任务',
    optionLabel: '执行STRM任务',
    icon: 'fas fa-film',
    desc: '触发已有 STRM 任务，扫描范围遵循任务自身配置',
    normalize: params => ({
      task_id: params.task_id ? Number(params.task_id) : '',
      run_mode: params.run_mode && params.run_mode !== 'auto' ? params.run_mode : 'full'
    }),
    canApply: action => Number(action.params.task_id || 0) > 0,
    nodeTitle: action => `STRM「${findTaskLabel('strm', action.params.task_id)}」`,
    previewTitle: action => `执行STRM任务[${findTaskLabel('strm', action.params.task_id)}]`
  },
  strm_scrape: {
    label: '生成本地STRM元数据',
    optionLabel: '生成本地STRM元数据',
    icon: 'fas fa-images',
    desc: '对该 STRM 任务执行本地元数据刮削',
    normalize: params => ({
      task_id: params.task_id ? Number(params.task_id) : '',
      write_mode: params.write_mode === 'overwrite' ? 'overwrite' : 'missing_only',
      failure_policy: ['any_failed', 'never'].includes(params.failure_policy) ? params.failure_policy : 'all_failed'
    }),
    canApply: action => Number(action.params.task_id || 0) > 0,
    nodeTitle: action => `刮削「${findTaskLabel('strm', action.params.task_id)}」`,
    previewTitle: action => `生成本地STRM元数据[${findTaskLabel('strm', action.params.task_id)}]`
  },
  delay: {
    label: '延迟',
    optionLabel: '延迟等待',
    icon: 'fas fa-clock',
    desc: '等待一段时间后再继续下一步',
    normalize: params => ({ seconds: Number(params.seconds || 60) }),
    canApply: action => Number(action.params.seconds || 0) > 0,
    nodeTitle: action => `延迟 ${Number(action.params.seconds || 60)} 秒`,
    previewTitle: action => `延迟${formatDelay(action.params.seconds)}`
  },
  emby_refresh: {
    label: 'Emby刷库',
    optionLabel: 'Emby全局刷库',
    icon: 'fas fa-server',
    desc: '通知 Emby 扫描全部媒体库，或只扫描指定媒体库',
    normalize: params => ({
      emby_id: String(params.emby_id || defaultEmbyConfig()?.id || ''),
      mode: params.mode === 'library' ? 'library' : 'global',
      library_id: String(params.library_id || ''),
      library_name: String(params.library_name || '')
    }),
    canApply: action => Boolean(findEmbyConfig(action?.params?.emby_id)?.emby_url) && (
      action?.params?.mode !== 'library' || Boolean(String(action?.params?.library_id || '').trim())
    ),
    nodeTitle: action => `Emby ${embyRefreshModeLabel(action)}「${embyRefreshTargetLabel(action)}」`,
    previewTitle: action => `Emby${embyRefreshModeLabel(action)}[${embyRefreshTargetLabel(action)}]`
  },
  emby_complete_media_info: {
    label: 'Emby 补全媒体信息',
    optionLabel: 'Emby 补全媒体信息',
    icon: 'fas fa-circle-info',
    desc: '检查媒体流信息缺失的条目，并通知 Emby 重新提取',
    normalize: params => ({
      emby_id: String(params.emby_id || defaultEmbyConfig()?.id || ''),
      mode: params.mode === 'library' ? 'library' : 'global',
      library_id: String(params.library_id || ''),
      library_name: String(params.library_name || '')
    }),
    canApply: action => Boolean(findEmbyConfig(action?.params?.emby_id)?.emby_url) && (
      action?.params?.mode !== 'library' || Boolean(String(action?.params?.library_id || '').trim())
    ),
    nodeTitle: action => `Emby 补全媒体信息「${embyRefreshTargetLabel(action)}」`,
    previewTitle: action => `Emby补全媒体信息[${embyRefreshTargetLabel(action)}]`
  }
}

const UNKNOWN_ACTION = {
  label: '未知动作',
  optionLabel: '未知动作',
  icon: 'fas fa-circle',
  desc: '',
  normalize: () => ({}),
  canApply: () => true,
  nodeTitle: () => '未知动作',
  previewTitle: () => '未知动作'
}

const actionDefinition = type => ACTION_DEFINITIONS[type] || UNKNOWN_ACTION
const actionTypeOptions = Object.entries(ACTION_DEFINITIONS).map(([value, definition]) => ({
  value,
  label: definition.optionLabel,
  desc: definition.desc
}))

const organizeTaskOptions = computed(() => options.value.organize_tasks.map(task => ({
  value: String(task.id),
  label: task.name || task.id
})))

const strmTaskOptions = computed(() => options.value.strm_tasks.map(task => ({
  value: Number(task.id),
  label: `${task.name || task.id}${task.branch_check_enabled ? '（分支）' : ''}`
})))

const strmScrapeWriteModeOptions = [
  { value: 'missing_only', label: '仅补缺（推荐）' },
  { value: 'overwrite', label: '覆盖已有 nfo / 海报' }
]

const strmScrapeFailurePolicyOptions = [
  { value: 'all_failed', label: '全部失败才中断（默认，推荐）' },
  { value: 'any_failed', label: '任一失败即中断' },
  { value: 'never', label: '失败也继续' }
]

const embyRefreshModeOptions = [
  { value: 'global', label: '全局媒体库扫描' },
  { value: 'library', label: '指定媒体库扫描' }
]

const embyConfigOptions = computed(() => (options.value.emby_configs || []).map(item => ({
  value: item.id,
  label: item.name
})))

const embyLibraryOptions = computed(() => embyLibraries.value.map(item => ({
  value: item.id,
  label: item.name
})))

const linkedActionItems = computed(() => (
  form.actions
    .map((action, index) => ({ action, index }))
    .filter(item => item.index > 0 && item.action)
))

const visibleLinkedActionItems = computed(() => (
  draggingActionIndex.value === null
    ? linkedActionItems.value
    : linkedActionItems.value.filter(item => item.index !== draggingActionIndex.value)
))

const isActiveDropIndex = (index) => {
  if (draggingActionIndex.value === null || actionDropIndex.value !== index) return false
  return index !== draggingActionIndex.value + 1
}

const dragGhostStyle = computed(() => ({
  left: `${dragGhost.x}px`,
  top: `${dragGhost.y}px`,
  width: `${dragGhost.width}px`
}))

const runningFlowSignature = computed(() => (
  rules.value
    .map(rule => `${rule.id}:${rule.is_running ? 1 : 0}:${rule.running_step?.index ?? ''}:${rule.actions?.length || 0}`)
    .join('|')
))

const triggerTime = computed(() => (
  form.trigger_type === 'daily'
    ? form.trigger_config.time
    : form.trigger_config.start_time
))

const triggerNodeTitle = computed(() => {
  if (!form.trigger_type) return '添加触发条件'
  if (form.trigger_type === 'interval') {
    return form.trigger_config.start_time
      ? `${form.trigger_config.start_time} 起，每 ${form.trigger_config.interval_hours || 24} 小时`
      : '本次触发时间 + 间隔'
  }
  if (form.trigger_type === 'external_event') {
    return form.trigger_config.event ? `收到通知：${form.trigger_config.event}` : '第三方通知'
  }
  if (form.trigger_type === 'offline_download') {
    return triggerReady.value ? `离线下载完成：${offlineDownloadDirectoryLabel.value}` : '离线下载完成'
  }
  return form.trigger_config.time ? `每天 ${form.trigger_config.time}` : '每天定时'
})

const triggerNodeSub = computed(() => (
  !form.trigger_type
    ? '时间 / 间隔触发'
    : form.trigger_type === 'interval'
    ? '从指定时间开始按间隔轮询执行'
    : form.trigger_type === 'external_event'
    ? externalEventSubtitle.value
    : form.trigger_type === 'offline_download'
    ? '任务目标位于所选目录或其子目录时触发'
    : '每天到点自动启动联动'
))

const offlineDownloadDirectoryLabel = computed(() => {
  const path = String(form.trigger_config.path || '').trim()
  if (!path) return '请选择账号和目录'
  const accountName = String(form.trigger_config.account_name || '').trim() || '网盘'
  return `${accountName} · ${path}`
})

const offlineFolderPickerAccountId = computed(() => {
  const accountId = Number(form.trigger_config.account_id || 0)
  return accountId > 0 ? accountId : null
})

const externalEventSubtitle = computed(() => {
  const parts = ['外部程序发来同名通知时触发']
  if (form.trigger_config.source) parts.push(`来源：${form.trigger_config.source}`)
  return parts.join(' · ')
})

const externalEventNameForExample = computed(() => String(form.trigger_config.event || '').trim() || 'download_completed')
const externalEventSourceForExample = computed(() => String(form.trigger_config.source || '').trim())
const externalEventPayloadForExample = computed(() => {
  const payload = {
    event: externalEventNameForExample.value,
    message: `${externalEventNameForExample.value}，请执行联动`
  }
  if (externalEventSourceForExample.value) {
    payload.source = externalEventSourceForExample.value
  }
  return payload
})
const externalEventJsonExample = computed(() => JSON.stringify(externalEventPayloadForExample.value, null, 2))

const hasValidationError = computed(() => validationIssues.value.some(issue => issue.level === 'error'))
const triggerReady = computed(() => {
  if (form.trigger_type === 'daily') return Boolean(form.trigger_config.time)
  if (form.trigger_type === 'interval') return Boolean(form.trigger_config.start_time) && Number(form.trigger_config.interval_hours || 0) > 0
  if (form.trigger_type === 'external_event') return Boolean(String(form.trigger_config.event || '').trim())
  if (form.trigger_type === 'offline_download') {
    return Number(form.trigger_config.account_id || 0) > 0 && Boolean(String(form.trigger_config.path || '').trim())
  }
  return false
})
const primaryActionReady = computed(() => Boolean(form.actions[0]))
const canSave = computed(() => form.name.trim() && triggerReady.value && primaryActionReady.value && !hasValidationError.value)
const configAction = computed(() => pendingConfigAction.value || form.actions[configActionIndex.value] || null)
const configCanApply = computed(() => {
  if (configMode.value === 'trigger' && form.trigger_type === 'external_event') {
    return Boolean(String(form.trigger_config.event || '').trim())
  }
  if (configMode.value === 'trigger' && form.trigger_type === 'offline_download') {
    return triggerReady.value
  }
  if (configMode.value === 'action' && configAction.value) {
    return actionDefinition(configAction.value.type).canApply(configAction.value)
  }
  return true
})
const configTitle = computed(() => {
  if (configMode.value === 'trigger') {
    if (form.trigger_type === 'daily') return '每天定时'
    if (form.trigger_type === 'external_event') return '第三方通知'
    if (form.trigger_type === 'offline_download') return '离线下载完成'
    return '本次触发时间 + 间隔'
  }
  return configAction.value ? actionLabel(configAction.value) : '配置动作'
})
const issueForAction = (actionIndex) => (
  validationIssues.value.find(issue => Number(issue.action_index) === Number(actionIndex)) || null
)
const flowPreviewItems = computed(() => {
  const items = []
  if (form.trigger_type) {
    items.push({
      type: 'trigger',
      order: 1,
      title: triggerNodeTitle.value,
      sub: triggerReady.value ? '触发联动' : '等待设置时间'
    })
  }
  if (!form.actions[0] && linkedActionItems.value.length > 0) {
    items.push({
      type: 'action',
      actionIndex: 0,
      order: items.length + 1,
      title: '请选择要执行的任务',
      sub: '',
      issue: {
        level: 'error',
        message: '缺少“就执行”动作，保存前需要重新选择一个任务'
      }
    })
  }
  form.actions.forEach((action, index) => {
    if (!action) return
    items.push({
      type: 'action',
      actionIndex: index,
      order: items.length + 1,
      title: previewActionTitle(action),
      sub: index === 0 ? '就执行' : conditionLabel(action.condition, index),
      issue: issueForAction(index)
    })
  })
  const unplacedIssue = validationIssues.value.find(issue => issue.level === 'error' && issue.action_index === undefined)
  if (unplacedIssue && items.length > 0) {
    items[items.length - 1].issue = items[items.length - 1].issue || unplacedIssue
  }
  return items
})

const loadAll = async () => {
  loading.value = true
  try {
    const [rulesData, optionsData, accountsData] = await Promise.all([
      fetchAutomationRules(),
      fetchAutomationOptions(),
      accountsApi.list()
    ])
    rules.value = rulesData || []
    options.value = { ...emptyOptions(), ...(optionsData || {}) }
    accounts.value = accountsData || []
    scheduleCenterRunningFlowSteps('auto')
  } catch (error) {
    toast.error('加载自动联动失败: ' + getApiErrorMessage(error, '请稍后重试'))
  } finally {
    loading.value = false
  }
}

const refreshRulesOnly = async () => {
  try {
    rules.value = await fetchAutomationRules()
    scheduleCenterRunningFlowSteps()
  } catch (error) {
    // 静默刷新失败不打扰用户，下一轮或手动操作会再次刷新。
  }
}

const loadOptionsOnly = async () => {
  const [data, accountsData] = await Promise.all([
    fetchAutomationOptions(),
    accountsApi.list()
  ])
  options.value = { ...emptyOptions(), ...(data || {}) }
  accounts.value = accountsData || []
}

const resetForm = () => {
  form.name = ''
  form.trigger_type = ''
  form.trigger_config = normalizeAutomationTriggerConfig()
  form.status = 'running'
  form.actions = []
  embyLibraries.value = []
  embyLibrariesLoading.value = false
  embyLibrariesLoaded.value = false
  embyLibrariesConfigID.value = ''
  validationIssues.value = []
  validationOk.value = false
}

const openBuilder = async (rule = null) => {
  await loadOptionsOnly()
  editingRule.value = rule
  resetForm()
  if (rule) {
    form.name = rule.name || ''
    form.trigger_type = rule.trigger_type === 'webhook' ? 'external_event' : (rule.trigger_type || '')
    form.trigger_config = normalizeAutomationTriggerConfig(rule.trigger_config)
    form.status = rule.status || 'running'
    form.actions = normalizeActions(rule.actions || [])
  }
  form.actions.forEach(action => ensureStrmRunMode(action))
  normalizeActionConditions()
  scheduleValidation()
  viewMode.value = 'builder'
}

const backToList = async () => {
  viewMode.value = 'list'
  editingRule.value = null
  resetForm()
  await loadAll()
}

const setTriggerType = (type) => {
  form.trigger_type = type
  if (type === 'daily') {
    form.trigger_config.start_time = ''
  } else if (type === 'interval') {
    form.trigger_config.time = ''
  } else if (type === 'external_event') {
    form.trigger_config.time = ''
    form.trigger_config.start_time = ''
  } else if (type === 'offline_download') {
    form.trigger_config.time = ''
    form.trigger_config.start_time = ''
  }
}

const openOfflineFolderPicker = () => {
  offlineFolderPickerVisible.value = true
}

const selectOfflineDownloadDirectory = (selection) => {
  form.trigger_config.account_id = Number(selection.accountId || 0)
  form.trigger_config.account_name = String(selection.accountName || '')
  form.trigger_config.parent_id = String(selection.parentId || '')
  form.trigger_config.path = String(selection.path || '/')
  offlineFolderPickerVisible.value = false
}

// 触发器未确认前不落地：进入选择/配置前快照，确认才提交，取消则还原（与执行动作的待确认机制对齐）
let triggerSnapshot = null
const snapshotTrigger = () => {
  triggerSnapshot = { type: form.trigger_type, config: { ...form.trigger_config } }
}
const restoreTrigger = () => {
  if (!triggerSnapshot) return
  form.trigger_type = triggerSnapshot.type
  form.trigger_config = { ...triggerSnapshot.config }
  triggerSnapshot = null
}
const commitTrigger = () => { triggerSnapshot = null }

const openTriggerPicker = () => {
  snapshotTrigger()
  pickerKind.value = 'trigger'
  pickerVisible.value = true
}

const openActionPicker = (insertIndex = form.actions.length) => {
  actionInsertIndex.value = Math.max(0, Math.min(Number(insertIndex) || 0, form.actions.length))
  pickerKind.value = 'action'
  pickerVisible.value = true
}

// 取消选择（X）：触发器选择被放弃时还原，不落地任何条件
const cancelPicker = () => {
  const wasTrigger = pickerKind.value === 'trigger'
  pickerVisible.value = false
  if (wasTrigger) restoreTrigger()
}

const openConfig = (mode, actionIndex = -1) => {
  configMode.value = mode
  configActionIndex.value = actionIndex
  if (actionIndex >= 0) ensureStrmRunMode(form.actions[actionIndex])
  if (mode === 'action') {
    const targetAction = pendingConfigAction.value || form.actions[actionIndex]
    if (isEmbyScopedAction(targetAction)) {
      normalizeEmbyScopedAction(targetAction)
      void ensureEmbyLibrariesLoaded()
    }
  }
  configVisible.value = true
}

const closeConfig = () => {
  // 触发器配置未确认就关闭：还原到打开前的状态（commitTrigger 后 snapshot 已清空，此处为安全兜底）
  if (configMode.value === 'trigger') restoreTrigger()
  configVisible.value = false
  configActionIndex.value = -1
  pendingConfigAction.value = null
  pendingConfigInsertIndex.value = -1
}

const chooseTrigger = (type) => {
  setTriggerType(type)
  pickerVisible.value = false
  if (type === 'daily') {
    openTimePicker()
    return
  }
  openConfig('trigger')
}

const chooseAction = (type) => {
  const action = createAction(type)
  pickerVisible.value = false
  if (type !== 'cache_clear') {
    pendingConfigAction.value = action
    pendingConfigInsertIndex.value = actionInsertIndex.value
    openConfig('action', -1)
    return
  }
  if (actionInsertIndex.value === 0 && !form.actions[0]) {
    if (form.actions.length === 0) {
      form.actions.push(action)
    } else {
      form.actions[0] = action
    }
  } else {
    form.actions.splice(Math.max(1, actionInsertIndex.value), 0, action)
  }
  normalizeActionConditions()
  scheduleValidation()
}

const openActionConfigFromCard = (index) => {
  if (suppressActionClick.value || draggingActionIndex.value !== null) return
  if (form.actions[index]?.type === 'cache_clear') return
  openConfig('action', index)
}

const toggleLinkedSortMode = () => {
  linkedSortMode.value = !linkedSortMode.value
  if (!linkedSortMode.value) cancelActionPointerDrag()
}

const startActionPointerDrag = (index, event) => {
  if (!linkedSortMode.value) return
  if (index <= 0) return
  if (event.pointerType === 'mouse' && event.button !== 0) return
  if (event.target?.closest?.('button, input, textarea, select, .node-del')) return
  const action = form.actions[index]
  if (!action) return
  const rect = event.currentTarget?.getBoundingClientRect?.()
  pendingActionDrag = {
    index,
    startX: event.clientX,
    startY: event.clientY,
    offsetX: rect ? event.clientX - rect.left : 18,
    offsetY: rect ? event.clientY - rect.top : 18
  }
  dragGhost.type = action.type
  dragGhost.title = actionNodeTitle(action)
  dragGhost.sub = actionNodeSub(action)
  dragGhost.width = rect?.width || 520
  dragGhost.x = rect?.left || event.clientX
  dragGhost.y = rect?.top || event.clientY
  dragGhost.visible = false
  document.addEventListener('pointermove', handleActionPointerMove)
  document.addEventListener('pointerup', finishActionPointerDrag, { once: true })
  document.addEventListener('pointercancel', cancelActionPointerDrag, { once: true })
}

const updateDragGhostPosition = (event) => {
  if (!pendingActionDrag) return
  dragGhost.x = event.clientX - pendingActionDrag.offsetX
  dragGhost.y = event.clientY - pendingActionDrag.offsetY
}

const showActionDragGhost = (event) => {
  updateDragGhostPosition(event)
  dragGhost.visible = true
}

const hideActionDragGhost = () => {
  dragGhost.visible = false
  dragGhost.type = ''
  dragGhost.title = ''
  dragGhost.sub = ''
  dragGhost.width = 0
  dragGhost.x = 0
  dragGhost.y = 0
}

const handleActionPointerMove = (event) => {
  if (!pendingActionDrag) return
  const dx = Math.abs(event.clientX - pendingActionDrag.startX)
  const dy = Math.abs(event.clientY - pendingActionDrag.startY)
  if (draggingActionIndex.value === null) {
    if (dx < 4 && dy < 4) return
    draggingActionIndex.value = pendingActionDrag.index
    actionDropIndex.value = pendingActionDrag.index
    suppressActionClick.value = true
    showActionDragGhost(event)
    document.body.classList.add('automation-action-dragging')
  }
  event.preventDefault()
  updateDragGhostPosition(event)
  updateActionDropFromPoint(event.clientX, event.clientY)
}

const updateActionDropFromPoint = (clientX, clientY) => {
  const dropEl = document.elementFromPoint(clientX, clientY)?.closest?.('[data-action-drop-index]')
  if (dropEl?.dataset?.actionDropIndex !== undefined) {
    actionDropIndex.value = Math.max(1, Math.min(Number(dropEl.dataset.actionDropIndex) || 1, form.actions.length))
    return
  }

  const actionEls = Array.from(document.querySelectorAll('.automation-page [data-action-index]'))
    .filter(el => Number(el.dataset.actionIndex) > 0)
  let target = Math.max(1, form.actions.length)
  for (const el of actionEls) {
    const rect = el.getBoundingClientRect()
    const index = Number(el.dataset.actionIndex)
    if (clientY < rect.top + rect.height / 2) {
      target = index
      break
    }
  }
  actionDropIndex.value = Math.max(1, Math.min(target, form.actions.length))
}

const dropActionAt = (insertIndex) => {
  if (draggingActionIndex.value === null) return
  const fromIndex = draggingActionIndex.value
  if (fromIndex <= 0) {
    endActionDrag()
    return
  }
  const targetIndex = Math.max(1, Math.min(Number(insertIndex) || 1, form.actions.length))
  if (targetIndex === fromIndex || targetIndex === fromIndex + 1) {
    endActionDrag()
    return
  }
  const moved = form.actions.splice(fromIndex, 1)[0]
  if (!moved) {
    endActionDrag()
    return
  }
  const normalizedTarget = fromIndex < targetIndex ? targetIndex - 1 : targetIndex
  form.actions.splice(normalizedTarget, 0, moved)
  normalizeActionConditions()
  scheduleValidation()
  endActionDrag()
}

const endActionDrag = () => {
  pendingActionDrag = null
  draggingActionIndex.value = null
  actionDropIndex.value = null
  hideActionDragGhost()
  document.removeEventListener('pointermove', handleActionPointerMove)
  document.removeEventListener('pointerup', finishActionPointerDrag)
  document.removeEventListener('pointercancel', cancelActionPointerDrag)
  document.body.classList.remove('automation-action-dragging')
  if (suppressActionClick.value) {
    window.setTimeout(() => {
      suppressActionClick.value = false
    }, 0)
  }
}

const finishActionPointerDrag = () => {
  if (draggingActionIndex.value !== null) {
    dropActionAt(actionDropIndex.value ?? draggingActionIndex.value)
    return
  }
  endActionDrag()
}

const cancelActionPointerDrag = () => {
  endActionDrag()
}

const applyConfig = () => {
  if (!configCanApply.value) {
    if (configMode.value === 'trigger' && form.trigger_type === 'external_event') {
      toast.warning('请输入通知名称')
    } else if (configMode.value === 'trigger' && form.trigger_type === 'offline_download') {
      toast.warning('请选择离线下载监控目录')
    } else if (configMode.value === 'action') {
      toast.warning('请完善动作配置')
    }
    return
  }
  if (configMode.value === 'trigger') commitTrigger()
  if (configAction.value) ensureStrmRunMode(configAction.value)
  if (isEmbyScopedAction(configAction.value)) normalizeEmbyScopedAction(configAction.value)
  if (pendingConfigAction.value) {
    const action = pendingConfigAction.value
    const insertIndex = pendingConfigInsertIndex.value
    if (insertIndex === 0 && !form.actions[0]) {
      if (form.actions.length === 0) {
        form.actions.push(action)
      } else {
        form.actions[0] = action
      }
    } else {
      form.actions.splice(Math.max(1, insertIndex), 0, action)
    }
    normalizeActionConditions()
  }
  scheduleValidation()
  closeConfig()
}

const openTimePicker = () => {
  timePickerValue.value = triggerTime.value || '00:00'
  timePickerVisible.value = true
}

const confirmTimePicker = (payload) => {
  const value = payload?.startTime || '00:00'
  if (form.trigger_type === 'daily') {
    form.trigger_config.time = value
  } else {
    form.trigger_config.start_time = value
  }
  timePickerVisible.value = false
  // 每天定时是直接经时间选择器确认的（无配置弹窗），此处即提交
  if (!configVisible.value) commitTrigger()
}

// 取消时间选择：若是“每天定时”的直接选择（未经配置弹窗），放弃则还原触发器
const cancelTimePicker = () => {
  timePickerVisible.value = false
  if (!configVisible.value) restoreTrigger()
}

const findStrmTask = (taskId) => (
  options.value.strm_tasks.find(task => Number(task.id) === Number(taskId)) || null
)

const getStrmRunModeOptions = (action) => {
  const modes = [
    { value: 'full', label: '完整扫描' }
  ]
  const task = action ? findStrmTask(action.params.task_id) : null
  if (task?.branch_check_enabled) {
    modes.push({ value: 'branch', label: '分支执行' })
  }
  return modes
}

const ensureStrmRunMode = (action) => {
  if (!action || action.type !== 'strm') return
  const task = findStrmTask(action.params.task_id)
  if (!action.params.run_mode || action.params.run_mode === 'auto') {
    action.params.run_mode = 'full'
  }
  if (action.params.run_mode === 'branch' && !task?.branch_check_enabled) {
    action.params.run_mode = 'full'
  }
}

const onStrmTaskChange = (action, taskId) => {
  action.params.task_id = Number(taskId)
  ensureStrmRunMode(action)
}

const findEmbyLibraryName = (libraryId) => (
  embyLibraries.value.find(item => String(item.id) === String(libraryId))?.name || ''
)

const defaultEmbyConfig = () => (
  (options.value.emby_configs || [])[0] || null
)

const findEmbyConfig = (embyId) => (
  (options.value.emby_configs || []).find(item => String(item.id) === String(embyId)) || null
)

const isEmbyScopedAction = action => ['emby_refresh', 'emby_complete_media_info'].includes(action?.type)

const normalizeEmbyScopedAction = (action) => {
  if (!isEmbyScopedAction(action)) return
  if (!findEmbyConfig(action.params.emby_id)) action.params.emby_id = defaultEmbyConfig()?.id || ''
  action.params.mode = action.params.mode === 'library' ? 'library' : 'global'
  if (action.params.mode !== 'library') {
    action.params.library_id = ''
    action.params.library_name = ''
    return
  }
  action.params.library_id = String(action.params.library_id || '').trim()
  action.params.library_name = findEmbyLibraryName(action.params.library_id) || String(action.params.library_name || '').trim()
}

const ensureEmbyLibrariesLoaded = async (force = false) => {
  const action = configAction.value
  const embyId = String(action?.params?.emby_id || '').trim()
  if (!findEmbyConfig(embyId)?.emby_url) return
  if (embyLibrariesLoading.value) return
  if (embyLibrariesLoaded.value && embyLibrariesConfigID.value === embyId && !force) return
  embyLibrariesLoading.value = true
  try {
    embyLibraries.value = await fetchEmbyLibraries(embyId)
    embyLibrariesLoaded.value = true
    embyLibrariesConfigID.value = embyId
  } catch (error) {
    if (force || !embyLibrariesLoaded.value) {
      toast.error('加载 Emby 媒体库失败: ' + getApiErrorMessage(error, '请检查 Emby 配置'))
    }
  } finally {
    embyLibrariesLoading.value = false
  }
}

const onEmbyConfigChange = (action, embyId) => {
  if (!isEmbyScopedAction(action)) return
  action.params.emby_id = String(embyId || '')
  action.params.library_id = ''
  action.params.library_name = ''
  embyLibraries.value = []
  embyLibrariesLoaded.value = false
  embyLibrariesConfigID.value = ''
  if (action.params.mode === 'library') void ensureEmbyLibrariesLoaded()
}

const onEmbyRefreshModeChange = (action, mode) => {
  if (!isEmbyScopedAction(action)) return
  action.params.mode = mode === 'library' ? 'library' : 'global'
  if (action.params.mode === 'library') {
    void ensureEmbyLibrariesLoaded()
  } else {
    action.params.library_id = ''
    action.params.library_name = ''
  }
}

const onEmbyLibraryChange = (action, libraryId) => {
  if (!isEmbyScopedAction(action)) return
  action.params.library_id = String(libraryId || '')
  action.params.library_name = findEmbyLibraryName(action.params.library_id)
}

const normalizeActions = (actions) => actions.map((action, index) => ({
  id: action.id || `${Date.now()}-${index}`,
  type: action.type,
  name: action.name || '',
  condition: index === 0 ? 'always' : (action.condition || 'prev_success'),
  params: normalizeParams(action.type, action.params || {})
}))

const normalizeParams = (type, params = {}) => actionDefinition(type).normalize(params)

const createAction = (type) => ({
  id: `${Date.now()}-${Math.random().toString(16).slice(2)}`,
  type,
  name: '',
  condition: form.actions.length === 0 ? 'always' : 'prev_success',
  params: normalizeParams(type)
})

const removeAction = (index) => {
  if (index === 0 && form.actions.length > 1) {
    form.actions[0] = null
  } else {
    form.actions.splice(index, 1)
  }
  if (linkedActionItems.value.length <= 1) {
    linkedSortMode.value = false
  }
  normalizeActionConditions()
  scheduleValidation()
}

const normalizeActionConditions = () => {
  // 首步无条件执行，其余步骤保留有效条件，缺失或非法时回落 prev_success。
  form.actions.forEach((action, index) => {
    if (!action) return
    if (index === 0) {
      action.condition = 'always'
    } else if (!['prev_success', 'prev_failed', 'always'].includes(action.condition)) {
      action.condition = 'prev_success'
    }
  })
}

const buildPayload = () => ({
  name: form.name.trim(),
  trigger_type: form.trigger_type === 'external_event' ? 'webhook' : form.trigger_type,
  trigger_config: serializeAutomationTriggerConfig(form.trigger_config),
  status: form.status,
  actions: form.actions.filter(Boolean).map((action, index) => ({
    id: action.id,
    type: action.type,
    name: action.name || '',
    condition: index === 0 ? 'always' : action.condition,
    params: normalizeParams(action.type, action.params)
  }))
})

const applyValidationResult = response => {
  validationIssues.value = response?.issues || []
  validationOk.value = response?.ok === true
}

const runRealtimeValidation = async () => {
  if (form.actions.filter(Boolean).length === 0) {
    validationIssues.value = []
    validationOk.value = false
    return
  }
  const seq = ++validationSeq
  try {
    const payload = buildPayload()
    const response = await validateAutomationRule(payload.actions)
    if (seq !== validationSeq) return
    applyValidationResult(response)
  } catch (error) {
    if (seq !== validationSeq) return
    validationIssues.value = [{ level: 'error', message: getApiErrorMessage(error, '联动性检查失败') }]
    validationOk.value = false
  }
}

const scheduleValidation = () => {
  if (validationTimer) clearTimeout(validationTimer)
  validationTimer = window.setTimeout(runRealtimeValidation, 350)
}

const saveRule = async () => {
  if (!form.name.trim()) {
    toast.warning('请输入联动名称')
    return
  }
  if (!triggerReady.value) {
    toast.warning('请完善触发条件')
    return
  }
  if (!form.actions[0]) {
    toast.warning('请先选择“就执行”的任务')
    return
  }
  saving.value = true
  try {
    const payload = buildPayload()
    const validateRes = await validateAutomationRule(payload.actions)
    applyValidationResult(validateRes)
    if (validateRes?.ok === false) {
      toast.error('当前联动不可保存，请检查联动条件')
      return
    }
    if (editingRule.value) {
      await updateAutomationRule(editingRule.value.id, payload)
    } else {
      await createAutomationRule(payload)
    }
    toast.success('自动联动已保存')
    await backToList()
  } catch (error) {
    toast.error('保存失败: ' + getApiErrorMessage(error, '请稍后重试'))
  } finally {
    saving.value = false
  }
}

const runRule = async (rule) => {
  try {
    await runAutomationRule(rule.id)
    toast.success('自动联动已开始执行')
    await loadAll()
  } catch (error) {
    toast.error('执行失败: ' + getApiErrorMessage(error, '请稍后重试'))
  }
}

const setRuleEnabled = async (rule, enabled) => {
  if ((rule.status === 'running') === enabled) return
  try {
    await toggleAutomationRule(rule.id)
    toast.success('状态已更新')
    await loadAll()
  } catch (error) {
    toast.error('状态更新失败: ' + getApiErrorMessage(error, '请稍后重试'))
  }
}

const deleteRule = async (rule) => {
  try {
    await confirm({
      title: '删除自动联动',
      message: `确认删除「${rule.name}」？运行记录也会一并清理。`,
      confirmText: '删除',
      danger: true,
      icon: 'trash'
    })
    await removeWithDust({
      target: findDustTarget(automationRuleList.value, `automation-rule-${rule.id}`),
      container: automationRuleList.value,
      remove: async () => {
        await deleteAutomationRule(rule.id)
        rules.value = rules.value.filter(item => item.id !== rule.id)
        runs.value = runs.value.filter(item => item.rule_id !== rule.id)
      }
    })
    toast.success('自动联动已删除')
    scheduleCenterRunningFlowSteps('auto')
  } catch (error) {
    if (error?.message !== 'Modal closed') {
      toast.error('删除失败: ' + getApiErrorMessage(error, '请稍后重试'))
    }
  }
}

const embyDisplayLabel = (action) => {
  const config = findEmbyConfig(action?.params?.emby_id) || defaultEmbyConfig()
  return config?.name || '未选择 Emby'
}

const embyRefreshModeLabel = (action) => (
  action?.params?.mode === 'library' ? '指定媒体库扫描' : '全局媒体库扫描'
)

const embyRefreshTargetLabel = (action) => {
  const embyName = embyDisplayLabel(action)
  if (action?.params?.mode === 'library') {
    const libraryName = findEmbyLibraryName(action?.params?.library_id) || String(action?.params?.library_name || '').trim() || '未选择媒体库'
    return `${embyName} · ${libraryName}`
  }
  return embyName
}

const findTaskLabel = (type, id) => {
  if (type === 'emby_refresh') {
    return embyDisplayLabel({ params: { emby_id: id } })
  }
  if (!id) return '未选择'
  if (type === 'organize') {
    return options.value.organize_tasks.find(task => String(task.id) === String(id))?.name || '整理任务'
  }
  if (type === 'strm') {
    return options.value.strm_tasks.find(task => Number(task.id) === Number(id))?.name || 'STRM任务'
  }
  return ''
}

const triggerLabel = (rule) => {
  const config = rule.trigger_config || {}
  if (rule.trigger_type === 'interval') {
    return `${config.start_time || '00:00'} 起，每 ${config.interval_hours || 24} 小时`
  }
  if (rule.trigger_type === 'external_event' || rule.trigger_type === 'webhook') {
    return `收到通知：${config.event || '-'}`
  }
  if (rule.trigger_type === 'offline_download') {
    const accountName = String(config.account_name || '').trim() || '网盘'
    return `离线下载完成：${accountName} · ${config.path || '/'}`
  }
  return `每天 ${config.time || '00:00'}`
}

const actionLabel = action => actionDefinition(action.type).label
const actionIcon = type => actionDefinition(type).icon
const actionNodeTitle = action => actionDefinition(action.type).nodeTitle(action)
const actionNodeSub = action => actionDefinition(action.type).desc

const formatDelay = (seconds) => {
  const value = Number(seconds || 60)
  if (value >= 60 && value % 60 === 0) return `${value / 60}分钟`
  return `${value}秒`
}

const previewActionTitle = action => actionDefinition(action.type).previewTitle(action)

const conditionLabel = (condition, index) => {
  if (index === 0) return '触发后立即'
  if (condition === 'prev_failed') return '上一步失败时'
  if (condition === 'always') return '无论上一步结果'
  return '上一步成功后'
}

const lastStatusLabel = (rule) => {
  if (rule.is_running) return '执行中'
  if (!rule.last_run_status) return '未运行'
  if (rule.last_run_status === 'success') return '成功'
  if (rule.last_run_status === 'running') return '执行中'
  return '失败'
}

const lastStatusVariant = (rule) => {
  if (rule.is_running || rule.last_run_status === 'running') return 'running'
  if (rule.last_run_status === 'success') return 'success'
  if (rule.last_run_status === 'failed') return 'error'
  return 'pending'
}

const isRuleActionRunning = (rule, actionIndex) => (
  Boolean(rule?.is_running) && Number(rule?.running_step?.index) === Number(actionIndex)
)

const setFlowViewportRef = (ruleId, el) => {
  const key = String(ruleId || '')
  if (!key) return
  if (el) {
    flowViewportRefs.set(key, el)
    scheduleCenterRunningFlowSteps('auto')
  } else {
    flowViewportRefs.delete(key)
  }
}

const centerRunningFlowSteps = (behavior = 'smooth') => {
  rules.value.forEach(rule => {
    const el = flowViewportRefs.get(String(rule.id || ''))
    if (!el) return
    if (!rule.is_running) {
      el.scrollLeft = 0
      return
    }
    const current = el.querySelector('.seg.running')
    if (!current) return
    const target = current.offsetLeft + current.offsetWidth / 2 - el.clientWidth / 2
    const maxScroll = Math.max(0, el.scrollWidth - el.clientWidth)
    el.scrollTo({
      left: Math.max(0, Math.min(maxScroll, target)),
      behavior
    })
  })
}

const scheduleCenterRunningFlowSteps = (behavior = 'smooth') => {
  if (flowCenterFrame) {
    window.cancelAnimationFrame(flowCenterFrame)
    flowCenterFrame = null
  }
  if (flowCenterTimer) {
    window.clearTimeout(flowCenterTimer)
    flowCenterTimer = null
  }
  flowCenterFrame = window.requestAnimationFrame(async () => {
    flowCenterFrame = null
    await nextTick()
    centerRunningFlowSteps(behavior)
    flowCenterTimer = window.setTimeout(() => {
      flowCenterTimer = null
      centerRunningFlowSteps('auto')
    }, 120)
  })
}

const refreshAndCenterRunningFlowSteps = () => {
  if (viewMode.value !== 'list') return
  refreshRulesOnly()
  scheduleCenterRunningFlowSteps('auto')
}

const handleVisibilityChange = () => {
  if (document.visibilityState === 'visible') refreshAndCenterRunningFlowSteps()
}

const handleWindowResize = () => {
  if (viewMode.value === 'list') scheduleCenterRunningFlowSteps('auto')
}

const runStepItems = (run) => {
  const steps = run?.result?.steps
  return Array.isArray(steps) ? steps : []
}

const stepStatusText = (status) => {
  if (status === 'success') return '完成'
  if (status === 'partial') return '部分完成'
  if (status === 'failed') return '失败'
  if (status === 'skipped') return '已跳过'
  return '执行中'
}

const runStatusText = (status) => {
  if (status === 'success') return '成功'
  if (status === 'running') return '执行中'
  return '失败'
}

const runSourceLabel = (source) => {
  if (source === 'manual') return '手动'
  if (source === 'external_event' || source === 'webhook') return '第三方'
  if (source === 'offline_download') return '离线下载'
  return '定时'
}

const ruleNameById = (ruleId) => {
  const target = String(ruleId || '')
  const hit = rules.value.find(rule => String(rule.id) === target)
  return hit?.name || '已删除的联动'
}

const openRuns = async () => {
  runs.value = []
  expandedRunIds.value = new Set()
  runsDrawerVisible.value = true
  runsLoading.value = true
  try {
    runs.value = await fetchAutomationRuns(undefined, 20)
    expandedRunIds.value = runs.value[0]?.id ? new Set([String(runs.value[0].id)]) : new Set()
  } catch (error) {
    toast.error('加载运行记录失败: ' + getApiErrorMessage(error, '请稍后重试'))
  } finally {
    runsLoading.value = false
  }
}

const isRunExpanded = (runId) => expandedRunIds.value.has(String(runId || ''))

const toggleRunExpanded = (runId) => {
  const key = String(runId || '')
  if (!key) return
  const next = new Set(expandedRunIds.value)
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
  }
  expandedRunIds.value = next
}

const clearRuns = async () => {
  if (runsLoading.value || runs.value.length === 0) return
  try {
    await confirm({
      title: '清空运行记录',
      message: '确认清空全部自动联动运行记录？该操作不会删除联动规则。',
      confirmText: '清空',
      danger: true,
      icon: 'trash'
    })
    await clearAutomationRuns()
    runs.value = []
    expandedRunIds.value = new Set()
    toast.success('运行记录已清空')
  } catch (error) {
    if (error?.message === 'Modal closed') return
    toast.error('清空运行记录失败: ' + getApiErrorMessage(error, '请稍后重试'))
  }
}

const closeRuns = () => {
  runsDrawerVisible.value = false
  runs.value = []
  expandedRunIds.value = new Set()
}

const formatDate = (value) => {
  if (!value) return '-'
  const formatted = formatTime(value)
  return formatted === '-' ? '-' : formatted.slice(0, 16)
}

const startPageActivity = () => {
  if (rulesRefreshTimer) return
  rulesRefreshTimer = window.setInterval(() => {
    if (viewMode.value === 'list') {
      refreshRulesOnly()
    }
  }, 5000)
  document.addEventListener('visibilitychange', handleVisibilityChange)
  window.addEventListener('focus', refreshAndCenterRunningFlowSteps)
  window.addEventListener('resize', handleWindowResize)
}

const stopPageActivity = () => {
  if (rulesRefreshTimer) clearInterval(rulesRefreshTimer)
  rulesRefreshTimer = null
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  window.removeEventListener('focus', refreshAndCenterRunningFlowSteps)
  window.removeEventListener('resize', handleWindowResize)
}

onMounted(() => {
  loadAll()
  startPageActivity()
})

let activatedOnce = false
onActivated(() => {
  startPageActivity()
  if (activatedOnce) void Promise.all([refreshRulesOnly(), loadOptionsOnly()])
  activatedOnce = true
})

onDeactivated(stopPageActivity)

watch(() => form.actions, () => {
  scheduleValidation()
}, { deep: true })

watch(runningFlowSignature, () => {
  scheduleCenterRunningFlowSteps()
}, { flush: 'post' })

watch(viewMode, mode => {
  if (mode === 'list') refreshAndCenterRunningFlowSteps()
}, { flush: 'post' })

onBeforeUnmount(() => {
  if (validationTimer) clearTimeout(validationTimer)
  stopPageActivity()
  if (flowCenterFrame) cancelAnimationFrame(flowCenterFrame)
  if (flowCenterTimer) clearTimeout(flowCenterTimer)
  flowViewportRefs.clear()
  cancelActionPointerDrag()
})

defineExpose({
  openCreate: () => openBuilder()
})
</script>

<style scoped>
:global(body.automation-action-dragging) {
  cursor: grabbing;
  user-select: none;
}

.automation-page {
  --panel: var(--surface);
  --soft: var(--surface-sunken);
  --line: var(--border);
  --line2: color-mix(in srgb, var(--border) 82%, var(--text-muted));
  --ink: var(--text);
  --muted: var(--text-muted);
  --muted2: color-mix(in srgb, var(--text-muted) 72%, transparent);
  --blue: var(--brand);
  --brand-grad: var(--brand-gradient);
  --ok: var(--success);
  --warn: var(--warning);
  --bad: var(--danger);
  --shadow: var(--shadow-card);
  color: var(--ink);
}

.node,
.card {
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--panel);
  box-shadow: var(--shadow);
}

.runs-drawer-overlay {
  --panel: var(--surface);
  --soft: var(--surface-sunken);
  --line: var(--border);
  --line2: color-mix(in srgb, var(--border) 82%, var(--text-muted));
  --ink: var(--text);
  --muted: var(--text-muted);
  --muted2: color-mix(in srgb, var(--text-muted) 72%, transparent);
  --ok: var(--success);
  --bad: var(--danger);
  --blue: var(--brand);
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.42);
  backdrop-filter: blur(3px);
  display: flex;
  justify-content: flex-end;
  z-index: 2800;
}

.runs-drawer {
  width: 420px;
  max-width: 92vw;
  height: 100%;
  background: var(--panel);
  display: flex;
  flex-direction: column;
  box-shadow: var(--shadow-pop);
  animation: runsDrawerIn 0.22s ease-out;
}

@keyframes runsDrawerIn {
  from { transform: translateX(24px); opacity: 0.6; }
  to { transform: translateX(0); opacity: 1; }
}

.runs-drawer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 20px;
  border-bottom: 1px solid var(--line);
  background: var(--soft);
}

.runs-drawer-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--ink);
}

.runs-drawer-sub {
  margin-top: 4px;
  font-size: 13px;
  color: var(--muted);
}

.runs-drawer-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.runs-clear-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  color: var(--muted);
  cursor: pointer;
  font-size: 12px;
  font-weight: 800;
}

.runs-clear-btn:hover:not(:disabled) {
  color: var(--bad);
  border-color: color-mix(in srgb, var(--bad) 28%, var(--line));
  background: color-mix(in srgb, var(--bad) 8%, var(--panel));
}

.runs-clear-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.runs-drawer-close {
  width: 32px;
  height: 32px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  color: var(--muted);
  cursor: pointer;
}

.runs-drawer-close:hover {
  color: var(--ink);
  border-color: var(--line2);
}

.runs-drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 16px 18px 22px;
}

.runs-drawer-empty {
  padding: 40px 0;
  text-align: center;
  color: var(--muted2);
  font-size: 13.5px;
}

.runs-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.runs-item {
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 12px;
  background: var(--panel);
  box-shadow: var(--shadow-card);
}

.runs-card-head {
  width: 100%;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 9px;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.runs-item-name {
  min-width: 0;
  font-size: 13.5px;
  color: var(--ink);
  font-weight: 850;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.runs-item-meta-line {
  grid-column: 1 / -1;
  margin-top: -2px;
  font-size: 12px;
  color: var(--muted2);
  white-space: nowrap;
}

.runs-status-mini {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  font-size: 11.5px;
  font-weight: 850;
  white-space: nowrap;
}

.runs-status-mini.success {
  background: color-mix(in srgb, var(--ok) 12%, var(--panel));
  color: var(--ok);
}

.runs-status-mini.running {
  background: color-mix(in srgb, var(--blue) 12%, var(--panel));
  color: var(--blue);
}

.runs-status-mini.failed {
  background: color-mix(in srgb, var(--bad) 12%, var(--panel));
  color: var(--bad);
}

.runs-expand-ico {
  color: var(--muted2);
  font-size: 11px;
  transition: transform 0.18s ease, color 0.18s ease;
}

.runs-expand-ico.open {
  color: var(--ink);
  transform: rotate(180deg);
}

.runs-steps {
  position: relative;
  display: grid;
  gap: 0;
  margin: 12px 0 0;
  padding: 12px 0 0;
  border-top: 1px solid var(--line);
  list-style: none;
}

.runs-step {
  position: relative;
  display: grid;
  grid-template-columns: 22px 1fr;
  gap: 9px;
  padding: 0 0 12px;
}

.runs-step::before {
  content: '';
  position: absolute;
  left: 10px;
  top: 21px;
  bottom: -1px;
  width: 2px;
  border-radius: 999px;
  background: var(--line);
}

.runs-step:last-child {
  padding-bottom: 0;
}

.runs-step:last-child::before {
  display: none;
}

.runs-step-dot {
  position: relative;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  margin-top: 0;
  border: 0;
  border-radius: 50%;
  background: var(--soft);
  color: var(--muted2);
  box-shadow: inset 0 0 0 1px var(--line);
}

.runs-step-dot::after {
  font-size: 11px;
  font-weight: 900;
  line-height: 1;
}

.runs-step-dot.success {
  background: color-mix(in srgb, var(--ok) 14%, var(--panel));
  color: var(--ok);
  box-shadow: inset 0 0 0 1px rgba(16, 185, 129, 0.18);
}

.runs-step-dot.success::after { content: '✓'; }

.runs-step-dot.failed {
  background: color-mix(in srgb, var(--bad) 10%, var(--panel));
  color: var(--bad);
  box-shadow: inset 0 0 0 1px rgba(239, 68, 68, 0.16);
}

.runs-step-dot.failed::after { content: '!'; }

.runs-step-dot.partial {
  background: color-mix(in srgb, var(--warn) 14%, var(--panel));
  color: var(--warn);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--warn) 24%, transparent);
}

.runs-step-dot.partial::after { content: '!'; }

.runs-step-dot.skipped {
  background: var(--soft);
  color: var(--muted2);
}

.runs-step-dot.skipped::after { content: '·'; }

.runs-step-body {
  min-width: 0;
  padding: 1px 0 2px;
}

.runs-step.failed .runs-step-body {
  margin: -5px 0 0 -4px;
  padding: 6px 8px 7px;
  border-radius: 8px;
  background: rgba(239, 68, 68, 0.025);
}

.runs-step-title {
  display: flex;
  align-items: baseline;
  gap: 6px;
  color: var(--ink);
  font-size: 12.5px;
  font-weight: 760;
}

.runs-step-title span {
  order: 2;
  color: var(--muted2);
  font-size: 11px;
  font-weight: 760;
}

.runs-step-title strong {
  min-width: 0;
  order: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runs-step-msg {
  margin-top: 2px;
  color: var(--muted);
  font-size: 11.8px;
  line-height: 1.45;
  word-break: break-all;
}

.runs-step.failed .runs-step-msg {
  color: var(--muted);
}

.panel-head-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--line);
  background: var(--soft);
}

.panel-title {
  font-size: 15.5px;
  font-weight: 800;
}

.panel-sub {
  margin-top: 3px;
  color: var(--muted);
  font-size: 12.5px;
  line-height: 1.5;
}

.table-wrap {
  overflow-x: auto;
}

.automation-table {
  min-width: 980px;
  table-layout: fixed;
}

.automation-row {
  position: relative;
}

.col-name { width: 15%; }
.col-flow { width: 42%; }
.col-last { width: 10%; }
.col-next { width: 13%; }
.col-op { width: 20%; }

.automation-table th.col-op {
  text-align: center;
}

.next-cell {
  white-space: nowrap;
}

.status-cell {
  min-width: 0;
}

.borrowable-cell {
  transform-origin: right center;
  transition: opacity 0.22s ease, transform 0.22s ease, filter 0.22s ease;
}

.rule-name {
  margin-bottom: 5px;
  color: var(--ink);
  font-weight: 800;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rule-desc {
  color: var(--muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.flowtext {
  position: relative;
  display: block;
  max-width: 100%;
  overflow-x: hidden;
  overflow-y: hidden;
  color: var(--muted);
  font-size: 12.5px;
  line-height: 1.6;
  white-space: nowrap;
  scroll-behavior: smooth;
  scrollbar-width: none;
  -webkit-mask-image: linear-gradient(90deg, #000 0, #000 calc(100% - 28px), transparent 100%);
  mask-image: linear-gradient(90deg, #000 0, #000 calc(100% - 28px), transparent 100%);
}

.flowtext::-webkit-scrollbar {
  display: none;
}

.flow-cell {
  position: relative;
  overflow: visible;
}

.flow-track {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
}

.flowtext-wide {
  position: absolute;
  left: 18px;
  top: 50%;
  z-index: 5;
  display: flex;
  align-items: center;
  width: calc(177.142% - 36px);
  height: 36px;
  padding: 0;
  overflow: hidden;
  color: var(--muted);
  font-size: 12.5px;
  line-height: 1;
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transform: translateY(-50%) scaleX(0.94);
  transform-origin: left center;
  transition: opacity 0.24s ease, transform 0.28s cubic-bezier(0.2, 0.76, 0.2, 1);
  -webkit-mask-image: linear-gradient(90deg, #000 0, #000 calc(100% - 28px), transparent 100%);
  mask-image: linear-gradient(90deg, #000 0, #000 calc(100% - 28px), transparent 100%);
}

.automation-row:not(.is-running):has(.flow-hover-zone:hover) .borrowable-cell {
  opacity: 0;
  filter: blur(1px);
  transform: translateX(18px) scaleX(0.82);
}

.automation-row:not(.is-running) .flow-hover-zone:hover .flowtext-wide {
  opacity: 1;
  transform: translateY(-50%) scaleX(1);
}

.automation-row:not(.is-running) .flow-hover-zone:hover .flowtext {
  opacity: 0;
}

.automation-row.is-running .flowtext {
  -webkit-mask-image: linear-gradient(90deg, transparent 0, #000 28px, #000 calc(100% - 28px), transparent 100%);
  mask-image: linear-gradient(90deg, transparent 0, #000 28px, #000 calc(100% - 28px), transparent 100%);
}

.flowtext .seg {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 6px;
  border-radius: 999px;
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}

.flowtext-wide .seg {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex: 0 0 auto;
  padding: 2px 6px;
  border-radius: 999px;
}

.flowtext .seg.running {
  background: color-mix(in srgb, var(--blue) 14%, var(--panel));
  color: var(--blue);
  box-shadow: inset 0 0 0 1px rgba(47, 125, 243, 0.16);
}

.flowtext .seg.running::after {
  content: '执行中';
  margin-left: 2px;
  padding: 1px 5px;
  border-radius: 999px;
  background: var(--blue);
  color: #fff;
  font-size: 10px;
  font-weight: 800;
}

.flowtext .arrow {
  margin: 0 6px;
  color: var(--muted2);
}

.flowtext-wide .arrow {
  flex: 0 0 auto;
  margin: 0 6px;
  color: var(--muted2);
}

.empty-cell {
  padding: 34px 16px !important;
  color: var(--muted2) !important;
  text-align: center !important;
}

.builder-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 340px;
  gap: 18px;
  align-items: start;
}

.sec-title {
  margin: 6px 2px 12px;
  color: var(--ink);
  font-size: 17px;
  font-weight: 800;
}

.sec-title .hint {
  margin-left: 8px;
  color: var(--muted2);
  font-size: 12px;
  font-weight: 400;
}

.after-title {
  margin-top: 20px;
}

.linked-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 22px;
}

.sort-mode-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 10px;
  border: 1px solid var(--line);
  border-radius: 9px;
  background: color-mix(in srgb, var(--panel) 72%, transparent);
  color: var(--muted);
  cursor: pointer;
  font-size: 12px;
  font-weight: 850;
  transition: all 0.18s ease;
}

.sort-mode-btn:hover,
.sort-mode-btn.active {
  border-color: color-mix(in srgb, var(--blue) 38%, var(--line));
  background: color-mix(in srgb, var(--blue) 12%, var(--panel));
  color: var(--blue);
}

.flow-list {
  display: grid;
  gap: 14px;
}

.action-drop-zone {
  display: grid;
  place-items: center;
  min-height: 18px;
  margin: -8px 0;
  border: 1.5px dashed transparent;
  border-radius: 14px;
  color: transparent;
  font-size: 12px;
  font-weight: 850;
  opacity: 0.18;
  pointer-events: auto;
  transition: all 0.16s ease;
}

.action-drop-zone.active {
  min-height: 92px;
  margin: 0;
  border-color: color-mix(in srgb, var(--blue) 42%, var(--line));
  background: color-mix(in srgb, var(--blue) 8%, var(--panel));
  color: var(--blue);
  opacity: 1;
  box-shadow: inset 0 0 0 1px rgba(47, 125, 243, 0.08);
}

.node {
  overflow: hidden;
  box-shadow: 0 4px 12px rgba(20, 30, 60, 0.04);
}

.action-node {
  position: relative;
  cursor: pointer;
  transition: opacity 0.16s ease, transform 0.16s ease, border-color 0.16s ease;
}

.primary-action {
  cursor: pointer;
}

.action-node.sortable {
  cursor: grab;
}

.action-node.sortable:active {
  cursor: grabbing;
}

.action-node.primary-action:active {
  cursor: pointer;
}

.action-drag-ghost {
  position: fixed;
  z-index: 3000;
  display: flex;
  align-items: center;
  gap: 13px;
  min-height: 92px;
  padding: 20px 22px;
  border: 1px solid color-mix(in srgb, var(--blue) 42%, var(--line));
  border-radius: 14px;
  background: color-mix(in srgb, var(--panel) 82%, transparent);
  box-shadow: 0 22px 46px rgba(31, 42, 61, 0.18);
  pointer-events: none;
  opacity: 0.88;
  backdrop-filter: blur(10px);
}

.node-main {
  display: flex;
  align-items: flex-start;
  gap: 13px;
  padding: 20px 22px;
}

.node-main.compact {
  align-items: center;
  cursor: pointer;
}

.add-node .node-main {
  align-items: center;
}

.node-main.compact:hover .node-title {
  color: var(--blue);
}

.node-ico {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 50px;
  width: 50px;
  height: 50px;
  border-radius: 12px;
  background: color-mix(in srgb, #6366f1 16%, var(--panel));
  color: #6366f1;
  font-size: 18px;
}

.node-ico.act {
  background: color-mix(in srgb, var(--blue) 16%, var(--panel));
  color: var(--blue);
}

.node-ico.cache_clear {
  background: color-mix(in srgb, var(--ok) 16%, var(--panel));
  color: var(--ok);
}

.node-ico.strm_scrape {
  background: color-mix(in srgb, #0ea5e9 16%, var(--panel));
  color: #0ea5e9;
}

.node-ico.delay {
  background: color-mix(in srgb, var(--warn) 16%, var(--panel));
  color: var(--warn);
}

.node-ico.emby_refresh,
.node-ico.emby_complete_media_info {
  background: color-mix(in srgb, #8b5cf6 18%, var(--panel));
  color: #8b5cf6;
}

.node-ico.add {
  background: color-mix(in srgb, var(--ok) 16%, var(--panel));
  color: var(--ok);
}

.node-body {
  flex: 1;
  min-width: 0;
}

.node-title {
  color: var(--ink);
  font-size: 16px;
  font-weight: 800;
  line-height: 1.3;
}

.node-title.ph {
  color: var(--blue);
}

.node-sub {
  margin-top: 5px;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.4;
}

.node-chev {
  color: var(--muted2);
  font-size: 18px;
}

.node-del {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 26px;
  width: 26px;
  height: 26px;
  margin-top: 8px;
  border: 0;
  border-radius: 50%;
  background: var(--soft);
  color: var(--muted2);
  cursor: pointer;
}

.node-del:hover {
  background: color-mix(in srgb, var(--bad) 16%, var(--panel));
  color: var(--bad);
}

.node-main.compact .node-del {
  margin-top: 0;
}

.ctrl,
.time-btn {
  width: 100%;
  height: 40px;
  padding: 0 12px;
  border: 1.5px solid var(--line);
  border-radius: 11px;
  background: var(--panel);
  color: var(--ink);
  outline: none;
  font-size: 14px;
}

.time-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  text-align: left;
}

.input-with-suffix {
  position: relative;
}

.input-with-suffix .ctrl {
  padding-right: 36px;
}

.input-with-suffix span {
  position: absolute;
  top: 50%;
  right: 12px;
  color: var(--muted);
  transform: translateY(-50%);
}

.add-node {
  border-style: dashed;
  background: color-mix(in srgb, var(--panel) 55%, transparent);
  box-shadow: none;
  cursor: pointer;
}

.choice-node,
.action-node {
  border-style: dashed;
  background: color-mix(in srgb, var(--panel) 72%, transparent);
}

.rail {
  position: sticky;
  top: 18px;
  display: grid;
}

.rail-back {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 11px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: color-mix(in srgb, var(--panel) 72%, transparent);
  color: var(--muted);
  font-size: 12px;
  font-weight: 850;
  cursor: pointer;
  box-shadow: 0 8px 18px rgba(31, 42, 61, 0.05);
  transition: all 0.18s ease;
}

.rail-back:hover {
  border-color: var(--line2);
  color: var(--blue);
  transform: translateY(-1px);
  box-shadow: 0 10px 20px rgba(47, 125, 243, 0.09);
}

.builder-save-footer {
  display: flex;
  justify-content: center;
  margin-top: 28px;
  padding: 8px 0 24px;
}

.save-combo {
  display: flex;
  width: min(560px, 100%);
  min-height: 48px;
  border: 1px solid var(--line);
  border-radius: 14px;
  overflow: hidden;
  background: var(--panel);
  box-shadow: 0 14px 30px rgba(31, 42, 61, 0.08);
}

.save-name-input {
  min-width: 0;
  flex: 1;
  height: 48px;
  border: 0;
  padding: 0 18px;
  outline: none;
  color: var(--ink);
  font-size: 14px;
  font-weight: 700;
}

.save-name-input::placeholder {
  color: var(--muted2);
  font-weight: 600;
}

.builder-save-footer .save-wide {
  flex: 0 0 118px;
  width: 118px;
  height: 48px;
  border-radius: 0;
  box-shadow: none;
}

.card {
  overflow: hidden;
  border-color: var(--line);
  box-shadow: 0 12px 28px rgba(31, 42, 61, 0.07);
}

.flow-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 16px 12px;
  border-bottom: 1px solid var(--line);
  background: var(--soft);
}

.card .ch {
  font-size: 14px;
  font-weight: 800;
}

.card .cb {
  padding: 16px;
}

.flow-preview {
  display: grid;
  gap: 0;
  padding: 10px 0 8px;
}

.flow-preview-item {
  position: relative;
  display: grid;
  grid-template-columns: 42px 1fr;
  gap: 16px;
  min-height: 76px;
}

.flow-preview-item:not(:last-child) {
  padding-bottom: 22px;
}

.flow-preview-item::before,
.flow-preview-item::after {
  content: "";
  position: absolute;
  left: 19px;
  width: 2px;
  border-radius: 999px;
  background: linear-gradient(180deg, rgba(16, 185, 129, 0.32), rgba(148, 163, 184, 0.26));
}

.flow-preview-item::before {
  top: 0;
  height: 8px;
}

.flow-preview-item::after {
  top: 48px;
  bottom: 8px;
}

.flow-preview-item:first-child::before,
.flow-preview-item:last-child::after {
  display: none;
}

.flow-index {
  position: relative;
  z-index: 1;
  display: inline-grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: linear-gradient(135deg, #22c55e, #10b981);
  color: #fff;
  font-size: 15px;
  font-weight: 900;
  box-shadow: 0 14px 28px rgba(16, 185, 129, 0.22);
}

.flow-preview-item.trigger .flow-index {
  background: linear-gradient(135deg, #3b82f6, #06b6d4);
  box-shadow: 0 14px 28px rgba(59, 130, 246, 0.22);
}

.flow-preview-item.error .flow-index {
  background: linear-gradient(135deg, #ef4444, #f97316);
  box-shadow: 0 14px 30px rgba(239, 68, 68, 0.24);
  animation: flow-error-pulse 1.35s ease-in-out infinite;
}

.flow-preview-item.error .flow-copy {
  animation: flow-error-nudge 1.35s ease-in-out infinite;
}

.flow-copy {
  min-width: 0;
  padding: 6px 0 0;
}

.flow-title {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--ink);
  font-size: 15px;
  font-weight: 850;
  line-height: 1.35;
  word-break: break-word;
}

.flow-sub {
  margin-top: 6px;
  color: var(--muted);
  font-size: 12.5px;
  line-height: 1.35;
}

.flow-error-icon {
  display: inline-grid;
  place-items: center;
  flex: 0 0 20px;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: #fee2e2;
  color: #dc2626;
  font-size: 11px;
  animation: flow-error-icon-pop 1.35s ease-in-out infinite;
}

.flow-error-text {
  color: #dc2626;
  font-size: 12.5px;
  font-weight: 700;
  line-height: 1.4;
}

.flow-ok-text {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-top: 8px;
  color: #059669;
  font-size: 12px;
  font-weight: 800;
}

@keyframes flow-error-pulse {
  0%, 100% {
    transform: scale(1);
    box-shadow: 0 14px 30px rgba(239, 68, 68, 0.24);
  }
  50% {
    transform: scale(1.06);
    box-shadow: 0 18px 38px rgba(239, 68, 68, 0.34);
  }
}

@keyframes flow-error-nudge {
  0%, 100% {
    transform: translateX(0);
  }
  45% {
    transform: translateX(2px);
  }
}

@keyframes flow-error-icon-pop {
  0%, 100% {
    transform: scale(1);
  }
  50% {
    transform: scale(1.12);
  }
}

.automation-scope {
  --panel: var(--surface);
  --soft: var(--surface-sunken);
  --line: var(--border);
  --ink: var(--text);
  --muted: var(--text-muted);
  --muted2: color-mix(in srgb, var(--text-muted) 72%, transparent);
  --blue: var(--brand);
  --ok: var(--success);
  --warn: var(--warning);
}

.modal-group {
  padding: 2px 20px 8px;
  color: var(--muted2);
  font-size: 12px;
}

.pick-list {
  padding-bottom: 14px;
}

.pick-option {
  display: flex;
  align-items: center;
  gap: 13px;
  width: 100%;
  padding: 12px 20px;
  border: 0;
  background: var(--panel);
  color: var(--ink);
  cursor: pointer;
  text-align: left;
}

.pick-option:hover {
  background: var(--soft);
}

.pick-option > span:nth-child(2) {
  flex: 1;
  min-width: 0;
}

.pick-option b,
.pick-option em {
  display: block;
  font-style: normal;
}

.pick-option b {
  font-size: 14.5px;
}

.pick-option em {
  margin-top: 2px;
  color: var(--muted2);
  font-size: 12px;
}

.pick-option > i {
  color: var(--muted2);
  font-size: 13px;
}

.pick-ico {
  display: inline-grid;
  place-items: center;
  flex: 0 0 38px;
  width: 38px;
  height: 38px;
  border-radius: 11px;
  background: color-mix(in srgb, var(--blue) 16%, var(--panel));
  color: var(--blue);
  font-size: 17px;
}

.pick-ico.trigger,
.pick-ico.interval {
  background: color-mix(in srgb, #6366f1 16%, var(--panel));
  color: #6366f1;
}

.pick-ico.external_event {
  background: color-mix(in srgb, var(--ok) 16%, var(--panel));
  color: var(--ok);
}

.pick-ico.cache_clear {
  background: color-mix(in srgb, var(--ok) 16%, var(--panel));
  color: var(--ok);
}

.pick-ico.strm_scrape {
  background: color-mix(in srgb, #0ea5e9 16%, var(--panel));
  color: #0ea5e9;
}

.pick-ico.delay {
  background: color-mix(in srgb, var(--warn) 16%, var(--panel));
  color: var(--warn);
}

.pick-ico.emby_refresh,
.pick-ico.emby_complete_media_info {
  background: color-mix(in srgb, #8b5cf6 18%, var(--panel));
  color: #8b5cf6;
}

.cfg-body {
  display: grid;
  gap: 16px;
  padding: 8px 20px 18px;
  overflow-y: auto;
}

.cfg-row label {
  display: block;
  margin-bottom: 8px;
  color: var(--text-regular);
  font-size: 13px;
  font-weight: 700;
}

.field-tip {
  margin-top: 7px;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.45;
}

.inline-link-btn {
  margin-top: 10px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--blue);
  cursor: pointer;
  font-size: 12px;
  font-weight: 700;
}

.inline-link-btn:disabled {
  color: var(--muted2);
  cursor: not-allowed;
}

.api-example {
  display: grid;
  gap: 7px;
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--soft);
}

.api-example-title {
  color: var(--text);
  font-size: 13px;
  font-weight: 800;
}

.api-example-text {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
}

.api-example code,
.api-example pre {
  display: block;
  margin: 0;
  padding: 8px 10px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--soft) 88%, var(--line));
  color: var(--ink);
  font-size: 12px;
  line-height: 1.35;
  white-space: pre-wrap;
  word-break: break-all;
}

@media (max-width: 1120px) {
  .builder-grid {
    grid-template-columns: 1fr;
  }

  .rail {
    position: static;
  }
}

@media (max-width: 720px) {
  /* 手机小屏：隐藏最长的「执行流程」列，缩短横向滚动距离 */
  .automation-table {
    min-width: 480px;
  }

  .automation-table .col-flow,
  .automation-row .flow-cell {
    display: none;
  }
}

</style>
