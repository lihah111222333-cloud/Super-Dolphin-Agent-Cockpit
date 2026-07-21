import React from "react";
import { File } from "lucide-react";
import { PromptEditorModal } from "./PromptPageViewPanels.jsx";
import {
  PageHeader,
  PromptPersonalizationOverview,
} from "./PromptPageProfile.jsx";
import {
  PromptCardsGrid,
  PromptStatusMessages,
} from "./PromptPageViewList.jsx";
import { textValue } from "./model/promptPageTextUtils.js";
import { PromptIntentWizardModal } from "./PromptPageWizard.jsx";

export function PromptPageLayout(props) {
  const showEmpty =
    !props.isProjectPending &&
    !props.loading &&
    !props.showBlockingError &&
    props.visibleItems.length === 0;
  const showCards =
    !props.isProjectPending && !props.loading && props.visibleItems.length > 0;
  return (
    <section className="console-page prompt-page">
      <PageHeader
        copy={props.copy}
        title={props.copy.title}
        subtitle={props.copy.subtitle}
        projectPath={props.cwd || props.projectPath}
      />
      <PromptPersonalizationOverview
        copy={props.copy}
        counts={props.counts}
        fallbackMode={props.fallbackMode}
        isProjectPending={props.isProjectPending}
        personalization={props.personalization}
      />
      <PromptStatusMessages
        {...props}
        onRetry={props.editorActions.retryPromptSync}
      />
      {showEmpty ? <PromptEmptyState copy={props.copy} /> : null}
      {showCards ? <PromptCardsGrid {...props} /> : null}
      {props.notice && !props.modals.editorOpen && !props.modals.wizardOpen ? (
        <div className="prompt-notice">{props.notice}</div>
      ) : null}
      {props.modals.editorOpen ? (
        <PromptEditorModal
          form={props.modals.form}
          notice={props.notice}
          saving={props.saving}
          onChange={props.setters.setForm}
          onClose={() => {
            props.setters.setEditorOpen(false);
            props.setters.setNotice("");
          }}
          onSave={props.editorActions.savePrompt}
        />
      ) : null}
      {props.modals.wizardOpen ? (
        <PromptIntentWizardModal
          key={promptWizardKey(props.modals.wizardDraft)}
          cwd={props.cwd}
          initialDraft={props.modals.wizardDraft}
          resolveLaunchPreferences={props.resolveLaunchPreferences}
          onClose={() => {
            props.setters.setWizardOpen(false);
            props.setters.setWizardDraft(null);
          }}
          onSaved={props.draftActions.handleWizardSaved}
        />
      ) : null}
    </section>
  );
}
function PromptEmptyState({ copy }) {
  return (
    <div className="empty-state prompt-empty">
      <File size={30} />
      <h3>{copy.emptyTitle}</h3>
      <p>{copy.emptyText}</p>
    </div>
  );
}
function promptWizardKey(draft) {
  return (
    textValue(draft?.draftKey) ||
    textValue(draft?.id) ||
    textValue(draft?.name) ||
    "new"
  );
}
