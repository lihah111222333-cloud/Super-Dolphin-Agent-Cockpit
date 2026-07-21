import React, { useState } from 'react';
import { errorMessage, firstText, objectValue, textValue } from '../../shared/pageShared.js';
import { renderWorkflowTemplateDraft, saveWorkflowTemplate, writeWorkflowMaterial } from '../services/workflowPageService.js';
import {
  enterpriseFieldHelp,
  enterpriseFieldLabel,
  enterpriseFieldOptions,
  enterpriseFieldPlaceholder,
  enterpriseNodeDependencies,
  enterpriseNodeKey,
  enterpriseNodeTitle,
  enterpriseOptionLabel,
  enterpriseOutputTypes,
  enterpriseSaveTemplatePayload,
  enterpriseTemplateDefaultValues,
  enterpriseTemplateDescription,
  enterpriseTemplateFields,
  enterpriseTemplateId,
  enterpriseTemplateTitle,
  enterpriseTemplateVersionNumber,
  firstPresent,
  renderEnterpriseTemplatePreview,
  uniqueWorkflowMaterialStamp,
} from '../services/workflowEnterpriseTemplateModel.js';

const WORKFLOW_MATERIAL_UPLOAD_PREFIX = 'reports/workflows/uploads';
const TEXT_MATERIAL_EXTENSIONS = new Set(['.csv', '.json', '.log', '.md', '.text', '.txt', '.xml', '.yaml', '.yml']);
const BINARY_MATERIAL_EXTENSIONS = new Set(['.doc', '.docx', '.pdf', '.ppt', '.pptx', '.xls', '.xlsx', '.zip']);

function enterpriseTemplateDraftPayload(template, formValues, workflowCwd) {
  return {
    runtime_context: { cwd: workflowCwd },
    templateId: enterpriseTemplateId(template),
    values: formValues,
    version: enterpriseTemplateVersionNumber(template),
  };
}

function EnterpriseTemplateForm(props) {
  const { onAdjustTemplate, onStartTemplate, onTemplateChanged, starting, template, workflowCwd } = props;
  const [formValues, setFormValues] = useState(() => enterpriseTemplateDefaultValues(template));
  const [formError, setFormError] = useState('');
  const [createStatus, setCreateStatus] = useState('');
  const [saveState, setSaveState] = useState({ saving: false, status: '' });
  const [uploadingFieldKey, setUploadingFieldKey] = useState('');
  const [uploadStatus, setUploadStatus] = useState('');
  const fields = enterpriseTemplateFields(template);
  const draftPreview = renderEnterpriseTemplatePreview(template, formValues);
  const validateForm = () => {
    const missing = missingEnterpriseRequiredFields(fields, formValues);
    if (missing.length > 0) {
      setFormError(`请先填写必填参数：${missing.join('、')}`);
      return false;
    }
    setFormError('');
    return true;
  };
  const startTemplate = () => {
    if (!validateForm()) return;
    setCreateStatus('');
    void (async () => {
      const result = await onStartTemplate({
        ...template,
        templateValues: formValues,
        selectedOutputFormat: formValues.output_format,
        draftPreview,
      });
      if (result?.ok) {
        setCreateStatus(result.warning ? '已创建并启动，后端提示：' + result.warning : '已创建并启动自动化');
      }
    })();
  };
  const adjustTemplate = () => {
    if (!validateForm()) return;
    void onAdjustTemplate({
      ...template,
      templateValues: formValues,
      selectedOutputFormat: formValues.output_format,
      draftPreview,
    });
  };
  const readFieldFiles = async (field, files) => {
    const items = files ? Array.from(files).filter(Boolean) : [];
    if (items.length === 0) return;
    setUploadingFieldKey(field.key);
    setUploadStatus('');
    setFormError('');
    try {
      const content = await uploadEnterpriseTemplateTextFiles({ field, files: items, template });
      setFormValues((current) => ({ ...current, [field.key]: content }));
      setUploadStatus(`已上传 ${items.length} 个材料文件`);
    } catch (err) {
      setFormError('上传材料失败：' + errorMessage(err));
    } finally {
      setUploadingFieldKey('');
    }
  };
  const saveTemplate = async () => {
    if (!validateForm()) return;
    const templateId = enterpriseTemplateId(template);
    if (!workflowCwd) {
      setFormError('保存模板失败：项目路径不可用，无法渲染 DAG 草案。');
      return;
    }
    setSaveState({ saving: true, status: '' });
    try {
      const rendered = await renderWorkflowTemplateDraft(enterpriseTemplateDraftPayload(template, formValues, workflowCwd));
      const draft = rendered?.draft;
      if (!draft) throw new Error('workflowTemplates/renderDag 未返回 DAG 草案');
      const saved = await saveWorkflowTemplate(enterpriseSaveTemplatePayload(template, draft));
      await onTemplateChanged?.(templateId);
      const savedVersion = Number(saved?.template?.version) || enterpriseTemplateVersionNumber(template) + 1;
      setSaveState({ saving: false, status: `模板已保存为 v${savedVersion}` });
    } catch (err) {
      setFormError('保存模板失败：' + errorMessage(err));
      setSaveState({ saving: false, status: '' });
    }
  };

  return (
    <section className="enterprise-template-workbench" aria-labelledby="enterprise-template-workbench-title">
      <div className="enterprise-workbench-heading">
        <div>
          <h2 id="enterprise-template-workbench-title">{enterpriseTemplateTitle(template)}</h2>
          <p>{enterpriseTemplateDescription(template)}</p>
        </div>
        <div className="enterprise-workbench-badges" aria-label="模板能力">
          <span>{textValue(template.business_flow || template.businessFlow)}</span>
          <span>{template.requires_review || template.requiresReview ? '含复核节点' : '无复核节点'}</span>
          <span>{template.supports_schedule || template.supportsSchedule ? '支持定时' : '手动触发'}</span>
        </div>
      </div>
      {formError ? <p className="danger-text" role="alert">{formError}</p> : null}
      {createStatus ? <p role="status" className="settings-status">{createStatus}</p> : null}
      {saveState.status ? <p role="status" className="settings-status">{saveState.status}</p> : null}
      {uploadStatus ? <p role="status" className="settings-status">{uploadStatus}</p> : null}
      <div className="enterprise-workbench-layout">
        <form className="enterprise-template-form" onSubmit={(event) => { event.preventDefault(); startTemplate(); }}>
          <h3>模板参数</h3>
          {fields.map((field) => (
            <EnterpriseTemplateField
              field={field}
              key={field.key}
              onFileSelect={(files) => { void readFieldFiles(field, files); }}
              onChange={(value) => setFormValues((current) => ({ ...current, [field.key]: value }))}
              outputTypes={enterpriseOutputTypes(template)}
              uploading={uploadingFieldKey === field.key}
              value={formValues[field.key]}
            />
          ))}
          <div className="enterprise-template-form-actions">
            <button type="submit" className="btn-dark" disabled={starting}>
              {starting ? '正在创建' : '创建工作流'}
            </button>
            <button type="button" className="btn-outline" disabled={starting} onClick={adjustTemplate}>
              用聊天调整
            </button>
            <button type="button" className="btn-outline" disabled={starting || saveState.saving} onClick={() => { void saveTemplate(); }}>
              {saveState.saving ? '保存中...' : '保存为模板'}
            </button>
          </div>
        </form>
        <EnterpriseTemplatePreview draft={draftPreview} template={template} />
      </div>
    </section>
  );
}

function EnterpriseTemplateField(props) {
  const { field, onChange, onFileSelect, outputTypes, uploading, value } = props;
  const label = enterpriseFieldLabel(field);
  const help = enterpriseFieldHelp(field);
  const commonProps = {
    'aria-label': label,
    id: `enterprise-template-field-${field.key}`,
    value: textValue(value),
    onChange: (event) => onChange(event.target.value),
  };
  return (
    <label className="enterprise-template-field" htmlFor={commonProps.id}>
      <span>{label}{field.required ? <em>必填</em> : null}</span>
      <EnterpriseTemplateInput
        commonProps={commonProps}
        field={field}
        onFileSelect={onFileSelect}
        onChange={onChange}
        outputTypes={outputTypes}
        uploading={uploading}
        value={value}
      />
      {help ? <small>{help}</small> : null}
    </label>
  );
}

function EnterpriseTemplateInput(props) {
  const { commonProps, field, onChange, onFileSelect, outputTypes, uploading, value } = props;
  if (field.type === 'textarea') {
    return <textarea {...commonProps} placeholder={enterpriseFieldPlaceholder(field)} rows={4} />;
  }
  if (field.type === 'select' || field.type === 'multi_select') {
    const options = enterpriseFieldOptions(field);
    const selectOptions = options.length > 0 ? options : outputTypes.map((format) => ({ value: format, label: { zh: format.toUpperCase() } }));
    return (
      <select {...commonProps}>
        {selectOptions.map((option) => (
          <option key={option.value} value={option.value}>{enterpriseOptionLabel(option)}</option>
        ))}
      </select>
    );
  }
  if (field.type === 'boolean') {
    return (
      <input
        aria-label={commonProps['aria-label']}
        checked={Boolean(value)}
        id={commonProps.id}
        onChange={(event) => onChange(event.target.checked)}
        type="checkbox"
      />
    );
  }
  if (field.type === 'number') {
    return <input {...commonProps} placeholder={enterpriseFieldPlaceholder(field)} type="number" />;
  }
  if (field.type === 'file_ref') {
    const handleDrop = (event) => {
      event.preventDefault();
      event.stopPropagation();
      onFileSelect?.(event.dataTransfer?.files);
    };
    return (
      <div
        className="enterprise-template-file-ref"
        onDragOver={(event) => {
          event.preventDefault();
          event.stopPropagation();
          if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
        }}
        onDrop={handleDrop}
      >
        <input {...commonProps} placeholder={enterpriseFieldPlaceholder(field)} type="text" />
        <input
          aria-label={`${commonProps['aria-label']}文件`}
          disabled={uploading}
          multiple
          onChange={(event) => onFileSelect?.(event.target.files)}
          type="file"
        />
        <span>{uploading ? '正在上传材料' : '拖拽材料到这里，或选择文件'}</span>
      </div>
    );
  }
  return <input {...commonProps} placeholder={enterpriseFieldPlaceholder(field)} type="text" />;
}

async function uploadEnterpriseTemplateTextFiles({ field, files, template }) {
  const paths = await Promise.all(files.map(async (file, index) => {
    const name = firstText(file?.name, file?.path, `material-${index + 1}.txt`);
    const content = await readEnterpriseTemplateTextFile(file, name);
    const path = enterpriseMaterialUploadPath(template, field, name, index);
    const saved = await writeWorkflowMaterial({
      path,
      content: `### ${name}\n${content}`,
    });
    const savedPath = textValue(saved?.path);
    if (!savedPath) throw new Error(`上传材料 ${name} 未返回 sharedfile 路径`);
    return savedPath;
  }));
  return paths.join('\n');
}

async function readEnterpriseTemplateTextFile(file, name) {
  if (!isReadableTextMaterial(file, name)) {
    throw new Error(`不支持直接读取二进制材料 ${name}，请先转成 txt、md、csv、json 或 yaml 文本文件`);
  }
  if (typeof file?.text !== 'function') {
    throw new Error(`无法读取文件 ${name}`);
  }
  const content = await file.text();
  if (!textValue(content)) {
    throw new Error(`文件 ${name} 没有可用文本内容`);
  }
  return content;
}

function isReadableTextMaterial(file, name) {
  const type = textValue(file?.type).toLowerCase();
  const extension = materialFileExtension(name);
  if (type.startsWith('text/')) return true;
  if (TEXT_MATERIAL_EXTENSIONS.has(extension)) return true;
  if (BINARY_MATERIAL_EXTENSIONS.has(extension)) return false;
  if (type && type !== 'application/json' && type !== 'application/xml' && type !== 'application/yaml' && type !== 'application/x-yaml') return false;
  return true;
}

function enterpriseMaterialUploadPath(template, field, name, index) {
  const templateSlug = sanitizeSharedFileSegment(enterpriseTemplateId(template).replace(/\//g, '-')) || 'template';
  const fieldSlug = sanitizeSharedFileSegment(field?.key) || 'materials';
  const stamp = uniqueWorkflowMaterialStamp();
  const safeName = sanitizeSharedFileSegment(name) || `material-${index + 1}.txt`;
  return `${WORKFLOW_MATERIAL_UPLOAD_PREFIX}/${templateSlug}/${fieldSlug}/${stamp}-${index + 1}-${safeName}.md`;
}

function sanitizeSharedFileSegment(value) {
  return stripControlCharacters(textValue(value))
    .replace(/[\\/:*?"<>|]+/g, '-')
    .replace(/\s+/g, '-')
    .replace(/^\.+|\.+$/g, '')
    .slice(0, 80);
}

function stripControlCharacters(value) {
  return Array.from(value).filter((char) => {
    const code = char.charCodeAt(0);
    return code >= 32 && code !== 127;
  }).join('');
}

function materialFileExtension(name) {
  const normalized = textValue(name).toLowerCase();
  const dot = normalized.lastIndexOf('.');
  if (dot < 0) return '';
  return normalized.slice(dot);
}

function EnterpriseTemplatePreview({ draft, template }) {
  const nodes = Array.isArray(draft.nodes) ? draft.nodes : [];
  const finalOutput = objectValue(firstPresent(draft.final_output, draft.finalOutput, template?.final_output, template?.finalOutput));
  return (
    <div className="enterprise-template-preview">
      <div className="enterprise-template-preview-head">
        <div>
          <h3>DAG 草案预览</h3>
          <p>{draft.title || enterpriseTemplateTitle(template)}</p>
        </div>
        <span>{draft.final_node_key || 'final_node_key'}</span>
      </div>
      <dl className="enterprise-template-preview-meta">
        <div>
          <dt>触发</dt>
          <dd>{firstText(draft.trigger, 'manual')}</dd>
        </div>
        <div>
          <dt>最终输出</dt>
          <dd>{firstText(finalOutput.path_template, finalOutput.pathTemplate)}</dd>
        </div>
      </dl>
      <ol className="enterprise-template-preview-nodes">
        {nodes.map((node) => {
          const key = enterpriseNodeKey(node);
          const isFinal = key === draft.final_node_key;
          const isReview = key.includes('review') || enterpriseNodeTitle(node).includes('复核');
          return (
            <li key={key}>
              <div>
                <strong>{enterpriseNodeTitle(node)}</strong>
                <span>{textValue(node.node_type || node.nodeType)} · {textValue(node.assigned_to || node.assignedTo)}</span>
              </div>
              <em>{enterpriseNodeDependencies(node).length ? `依赖 ${enterpriseNodeDependencies(node).join('、')}` : '起始节点'}</em>
              {isReview ? <b>复核</b> : null}
              {isFinal ? <b>最终</b> : null}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function missingEnterpriseRequiredFields(fields, values) {
  return fields.reduce((missing, field) => {
    if (field.required && !textValue(values?.[field.key])) missing.push(enterpriseFieldLabel(field));
    return missing;
  }, []);
}

export { EnterpriseTemplateForm };
