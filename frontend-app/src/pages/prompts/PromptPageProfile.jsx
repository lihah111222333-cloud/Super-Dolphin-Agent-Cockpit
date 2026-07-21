import React from "react";
import { FileText, Upload, User } from "lucide-react";
import { emptyPersonalizationProfile } from "./model/promptPageProfileUtils.js";

export function PageHeader({ copy, title, subtitle, projectPath }) {
  return (
    <header className="prompt-header">
      {" "}
      <div>
        {" "}
        <h1>
          <FileText size={25} /> {title}
        </h1>{" "}
        {subtitle ? <strong>{subtitle}</strong> : null}{" "}
        <p title={projectPath}>
          {copy.currentProject}: {projectPath || copy.unknownProject}
        </p>{" "}
      </div>{" "}
    </header>
  );
}
export function PromptPersonalizationOverview({
  copy,
  counts,
  isProjectPending,
  fallbackMode,
  personalization,
}) {
  const metrics = [
    [copy.expert, counts.expert || 0],
    [copy.recall, counts.recall || 0],
    [copy.defaultRule, counts.default_rule || 0],
    [copy.pending, counts.pending || 0],
  ];
  const profile = personalization?.profile || emptyPersonalizationProfile;
  const profileLoading = Boolean(personalization?.loading);
  const profileSaving = Boolean(personalization?.saving);
  const profileErrors =
    personalization && personalization.validationErrors
      ? personalization.validationErrors
      : {};
  const profileInvalid = Object.keys(profileErrors).length > 0;
  // 无项目：按钮保持可聚焦可点击，点击后由 onProjectRequired 显示明确引导（不再永久禁用且原因不明）。
  // 表单无效：禁用保存，但每个字段下方显示具体校验原因；加载/保存中为瞬态禁用。
  const saveProfileDisabled =
    !isProjectPending && (profileLoading || profileSaving || profileInvalid);
  const updateProfile = (key) => (event) => {
    personalization?.onProfileChange?.({
      ...profile,
      [key]: event.target.value,
    });
  };
  const profileStatus = isProjectPending
    ? copy.waitingProject
    : personalization?.error
      ? copy.loadFailed
      : profileLoading
        ? copy.loadingShort
        : copy.connected;
  const overviewText = isProjectPending
    ? copy.overviewConnecting
    : fallbackMode
      ? copy.overviewFallback
      : copy.overviewReady;
  const handleSaveClick = () => {
    if (isProjectPending) {
      personalization?.onProjectRequired?.();
      return;
    }
    if (saveProfileDisabled) return;
    personalization?.onSaveProfile?.();
  };
  const handleImportClick = () => {
    if (isProjectPending) {
      personalization?.onProjectRequired?.();
      return;
    }
    personalization?.onImportMemory?.();
  };
  const fieldError = (key) =>
    profileErrors[key] ? (
      <span className="personalization-field-error" role="alert">
        {profileErrors[key]}
      </span>
    ) : null;
  return (
    <section
      className="personalization-overview"
      aria-label={copy.overviewAria}
    >
      <div className="personalization-overview-hero fusion-surface">
        <div className="personalization-overview-copy">
          <span>{copy.profile}</span>
          <h2>{copy.overviewTitle}</h2>
          <p>{overviewText}</p>
        </div>
        <dl>
          {metrics.map(([label, value]) => (
            <div key={label} className="fusion-surface-glass">
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      </div>
      <PromptProfileCards
        copy={copy}
        fieldError={fieldError}
        handleImportClick={handleImportClick}
        handleSaveClick={handleSaveClick}
        isProjectPending={isProjectPending}
        profile={profile}
        profileLoading={profileLoading}
        profileSaving={profileSaving}
        profileStatus={profileStatus}
        saveProfileDisabled={saveProfileDisabled}
        updateProfile={updateProfile}
      />
    </section>
  );
}

function PromptProfileCards(input) {
  const {
    copy,
    fieldError,
    handleImportClick,
    handleSaveClick,
    isProjectPending,
    profile,
    profileLoading,
    profileSaving,
    profileStatus,
    saveProfileDisabled,
    updateProfile,
  } = input;
  return (
    <div className="personalization-overview-content personalization-profile-grid">
      <section
        className="personalization-profile-card"
        aria-label={copy.profile}
      >
        <header>
          <h3>
            <User size={18} className="personalization-profile-icon" />{" "}
            {copy.profile}
          </h3>
          <span>{profileStatus}</span>
        </header>
        <div className="personalization-form-grid">
          <label>
            {copy.displayName}
            <input
              aria-label={copy.displayName}
              type="text"
              value={profile.displayName}
              onChange={updateProfile("displayName")}
              disabled={isProjectPending || profileLoading}
            />
            {fieldError("displayName")}
          </label>
          <label>
            {copy.role}
            <input
              aria-label={copy.role}
              type="text"
              value={profile.role}
              onChange={updateProfile("role")}
              disabled={isProjectPending || profileLoading}
            />
            {fieldError("role")}
          </label>
          <label>
            {copy.background}
            <textarea
              aria-label={copy.background}
              rows={3}
              value={profile.background}
              onChange={updateProfile("background")}
              disabled={isProjectPending || profileLoading}
            />
            {fieldError("background")}
          </label>
          <label>
            {copy.customInstructions}
            <textarea
              aria-label={copy.customInstructions}
              rows={3}
              value={profile.customInstructions}
              onChange={updateProfile("customInstructions")}
              disabled={isProjectPending || profileLoading}
            />
            {fieldError("customInstructions")}
          </label>
        </div>
        <button
          type="button"
          aria-disabled={isProjectPending || undefined}
          disabled={saveProfileDisabled}
          title={isProjectPending ? "请先选择项目" : undefined}
          onClick={handleSaveClick}
        >
          {profileSaving ? copy.saving : copy.saveProfile}
        </button>
      </section>
      <section
        className="personalization-profile-card suiyuan-import-memory-card"
        aria-label={copy.importMemoryTitle}
      >
        <div className="suiyuan-import-memory-content">
          <Upload size={28} className="suiyuan-import-memory-icon" />
          <h3>{copy.importMemoryTitle}</h3>
          <p>{copy.importMemoryText}</p>
          <button
            type="button"
            aria-disabled={isProjectPending || undefined}
            title={isProjectPending ? "请先选择项目" : undefined}
            onClick={handleImportClick}
          >
            {copy.importMemory}
          </button>
        </div>
      </section>
    </div>
  );
}
