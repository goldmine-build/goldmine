import {createTwirpRequest, throwTwirpError, Fetch} from './twirp';

export enum PreUploadStep {
  ANGLE_CODE_GENERATION = "ANGLE_CODE_GENERATION",
  ANGLE_GN_TO_BP = "ANGLE_GN_TO_BP",
  ANGLE_ROLL_CHROMIUM = "ANGLE_ROLL_CHROMIUM",
  GO_GENERATE_CIPD = "GO_GENERATE_CIPD",
  FLUTTER_LICENSE_SCRIPTS = "FLUTTER_LICENSE_SCRIPTS",
  FLUTTER_LICENSE_SCRIPTS_FOR_DART = "FLUTTER_LICENSE_SCRIPTS_FOR_DART",
  FLUTTER_LICENSE_SCRIPTS_FOR_FUCHSIA = "FLUTTER_LICENSE_SCRIPTS_FOR_FUCHSIA",
  SKIA_GN_TO_BP = "SKIA_GN_TO_BP",
  TRAIN_INFRA = "TRAIN_INFRA",
  UPDATE_FLUTTER_DEPS_FOR_DART = "UPDATE_FLUTTER_DEPS_FOR_DART",
}

export enum CommitMsgConfig_BuiltIn {
  DEFAULT = "DEFAULT",
  ANDROID = "ANDROID",
}

export enum GerritConfig_Config {
  ANDROID = "ANDROID",
  ANGLE = "ANGLE",
  CHROMIUM = "CHROMIUM",
  CHROMIUM_NO_CQ = "CHROMIUM_NO_CQ",
  LIBASSISTANT = "LIBASSISTANT",
}

export enum NotifierConfig_LogLevel {
  SILENT = "SILENT",
  ERROR = "ERROR",
  WARNING = "WARNING",
  INFO = "INFO",
  DEBUG = "DEBUG",
}

export enum NotifierConfig_MsgType {
  ISSUE_UPDATE = "ISSUE_UPDATE",
  LAST_N_FAILED = "LAST_N_FAILED",
  MODE_CHANGE = "MODE_CHANGE",
  NEW_FAILURE = "NEW_FAILURE",
  NEW_SUCCESS = "NEW_SUCCESS",
  ROLL_CREATION_FAILED = "ROLL_CREATION_FAILED",
  SAFETY_THROTTLE = "SAFETY_THROTTLE",
  STRATEGY_CHANGE = "STRATEGY_CHANGE",
  SUCCESS_THROTTLE = "SUCCESS_THROTTLE",
}

export interface Config {
  rollerName: string;
  childDisplayName: string;
  parentDisplayName: string;
  parentWaterfall: string;
  ownerPrimary: string;
  ownerSecondary: string;
  contacts?: string[];
  serviceAccount: string;
  isInternal: boolean;
  reviewer?: string[];
  reviewerBackup?: string[];
  rollCooldown?: google_protobuf_Duration;
  timeWindow: string;
  supportsManualRolls: boolean;
  commitMsg?: CommitMsgConfig;
  gerrit?: GerritConfig;
  github?: GitHubConfig;
  google3?: Google3Config;
  kubernetes?: KubernetesConfig;
  parentChildRepoManager?: ParentChildRepoManagerConfig;
  androidRepoManager?: AndroidRepoManagerConfig;
  commandRepoManager?: CommandRepoManagerConfig;
  freetypeRepoManager?: FreeTypeRepoManagerConfig;
  fuchsiaSdkAndroidRepoManager?: FuchsiaSDKAndroidRepoManagerConfig;
  google3RepoManager?: Google3RepoManagerConfig;
  notifiers?: NotifierConfig[];
  safetyThrottle?: ThrottleConfig;
  transitiveDeps?: TransitiveDepConfig[];
}

interface ConfigJSON {
  roller_name?: string;
  child_display_name?: string;
  parent_display_name?: string;
  parent_waterfall?: string;
  owner_primary?: string;
  owner_secondary?: string;
  contacts?: string[];
  service_account?: string;
  is_internal?: boolean;
  reviewer?: string[];
  reviewer_backup?: string[];
  roll_cooldown?: google_protobuf_DurationJSON;
  time_window?: string;
  supports_manual_rolls?: boolean;
  commit_msg?: CommitMsgConfigJSON;
  gerrit?: GerritConfigJSON;
  github?: GitHubConfigJSON;
  google3?: Google3ConfigJSON;
  kubernetes?: KubernetesConfigJSON;
  parent_child_repo_manager?: ParentChildRepoManagerConfigJSON;
  android_repo_manager?: AndroidRepoManagerConfigJSON;
  command_repo_manager?: CommandRepoManagerConfigJSON;
  freetype_repo_manager?: FreeTypeRepoManagerConfigJSON;
  fuchsia_sdk_android_repo_manager?: FuchsiaSDKAndroidRepoManagerConfigJSON;
  google3_repo_manager?: Google3RepoManagerConfigJSON;
  notifiers?: NotifierConfigJSON[];
  safety_throttle?: ThrottleConfigJSON;
  transitive_deps?: TransitiveDepConfigJSON[];
}

export interface CommitMsgConfig {
  bugProject: string;
  childLogUrlTmpl: string;
  cqExtraTrybots?: string[];
  cqDoNotCancelTrybots: boolean;
  includeLog: boolean;
  includeRevisionCount: boolean;
  includeTbrLine: boolean;
  includeTests: boolean;
  builtIn: CommitMsgConfig_BuiltIn;
  custom: string;
}

interface CommitMsgConfigJSON {
  bug_project?: string;
  child_log_url_tmpl?: string;
  cq_extra_trybots?: string[];
  cq_do_not_cancel_trybots?: boolean;
  include_log?: boolean;
  include_revision_count?: boolean;
  include_tbr_line?: boolean;
  include_tests?: boolean;
  built_in?: string;
  custom?: string;
}

export interface GerritConfig {
  url: string;
  project: string;
  config: GerritConfig_Config;
}

interface GerritConfigJSON {
  url?: string;
  project?: string;
  config?: string;
}

export interface GitHubConfig {
  repoOwner: string;
  repoName: string;
  checksWaitFor?: string[];
}

interface GitHubConfigJSON {
  repo_owner?: string;
  repo_name?: string;
  checks_wait_for?: string[];
}

export interface Google3Config {
}

interface Google3ConfigJSON {
}

export interface KubernetesConfig {
  cpu: string;
  memory: string;
  readinessFailureThreshold: number;
  readinessInitialDelaySeconds: number;
  readinessPeriodSeconds: number;
  disk: string;
  secrets?: KubernetesSecret[];
}

interface KubernetesConfigJSON {
  cpu?: string;
  memory?: string;
  readiness_failure_threshold?: number;
  readiness_initial_delay_seconds?: number;
  readiness_period_seconds?: number;
  disk?: string;
  secrets?: KubernetesSecretJSON[];
}

export interface KubernetesSecret {
  name: string;
  mountPath: string;
}

interface KubernetesSecretJSON {
  name?: string;
  mount_path?: string;
}

export interface AndroidRepoManagerConfig_ProjectMetadataFileConfig {
  filePath: string;
  name: string;
  description: string;
  homePage: string;
  gitUrl: string;
  licenseType: string;
}

interface AndroidRepoManagerConfig_ProjectMetadataFileConfigJSON {
  file_path?: string;
  name?: string;
  description?: string;
  home_page?: string;
  git_url?: string;
  license_type?: string;
}

export interface AndroidRepoManagerConfig {
  childRepoUrl: string;
  childBranch: string;
  childPath: string;
  parentRepoUrl: string;
  parentBranch: string;
  childRevLinkTmpl: string;
  childSubdir: string;
  preUploadSteps?: PreUploadStep[];
  metadata?: AndroidRepoManagerConfig_ProjectMetadataFileConfig;
}

interface AndroidRepoManagerConfigJSON {
  child_repo_url?: string;
  child_branch?: string;
  child_path?: string;
  parent_repo_url?: string;
  parent_branch?: string;
  child_rev_link_tmpl?: string;
  child_subdir?: string;
  pre_upload_steps?: string[];
  metadata?: AndroidRepoManagerConfig_ProjectMetadataFileConfigJSON;
}

export interface CommandRepoManagerConfig_CommandConfig {
  command?: string[];
  dir: string;
  env?: CommandRepoManagerConfig_CommandConfig_EnvEntry[];
}

interface CommandRepoManagerConfig_CommandConfigJSON {
  command?: string[];
  dir?: string;
  env?: CommandRepoManagerConfig_CommandConfig_EnvEntryJSON[];
}

export interface CommandRepoManagerConfig {
  gitCheckout?: GitCheckoutConfig;
  shortRevRegex: string;
  getTipRev?: CommandRepoManagerConfig_CommandConfig;
  getPinnedRev?: CommandRepoManagerConfig_CommandConfig;
  setPinnedRev?: CommandRepoManagerConfig_CommandConfig;
}

interface CommandRepoManagerConfigJSON {
  git_checkout?: GitCheckoutConfigJSON;
  short_rev_regex?: string;
  get_tip_rev?: CommandRepoManagerConfig_CommandConfigJSON;
  get_pinned_rev?: CommandRepoManagerConfig_CommandConfigJSON;
  set_pinned_rev?: CommandRepoManagerConfig_CommandConfigJSON;
}

export interface FreeTypeRepoManagerConfig {
  parent?: FreeTypeParentConfig;
  child?: GitilesChildConfig;
}

interface FreeTypeRepoManagerConfigJSON {
  parent?: FreeTypeParentConfigJSON;
  child?: GitilesChildConfigJSON;
}

export interface FuchsiaSDKAndroidRepoManagerConfig {
  parent?: GitilesParentConfig;
  child?: FuchsiaSDKChildConfig;
  genSdkBpRepo: string;
  genSdkBpBranch: string;
}

interface FuchsiaSDKAndroidRepoManagerConfigJSON {
  parent?: GitilesParentConfigJSON;
  child?: FuchsiaSDKChildConfigJSON;
  gen_sdk_bp_repo?: string;
  gen_sdk_bp_branch?: string;
}

export interface Google3RepoManagerConfig {
  childBranch: string;
  childRepo: string;
}

interface Google3RepoManagerConfigJSON {
  child_branch?: string;
  child_repo?: string;
}

export interface ParentChildRepoManagerConfig {
  copyParent?: CopyParentConfig;
  depsLocalGithubParent?: DEPSLocalGitHubParentConfig;
  depsLocalGerritParent?: DEPSLocalGerritParentConfig;
  gitCheckoutGithubFileParent?: GitCheckoutGitHubFileParentConfig;
  gitilesParent?: GitilesParentConfig;
  cipdChild?: CIPDChildConfig;
  fuchsiaSdkChild?: FuchsiaSDKChildConfig;
  gitCheckoutChild?: GitCheckoutChildConfig;
  gitCheckoutGithubChild?: GitCheckoutGitHubChildConfig;
  gitilesChild?: GitilesChildConfig;
  semverGcsChild?: SemVerGCSChildConfig;
  buildbucketRevisionFilter?: BuildbucketRevisionFilterConfig;
}

interface ParentChildRepoManagerConfigJSON {
  copy_parent?: CopyParentConfigJSON;
  deps_local_github_parent?: DEPSLocalGitHubParentConfigJSON;
  deps_local_gerrit_parent?: DEPSLocalGerritParentConfigJSON;
  git_checkout_github_file_parent?: GitCheckoutGitHubFileParentConfigJSON;
  gitiles_parent?: GitilesParentConfigJSON;
  cipd_child?: CIPDChildConfigJSON;
  fuchsia_sdk_child?: FuchsiaSDKChildConfigJSON;
  git_checkout_child?: GitCheckoutChildConfigJSON;
  git_checkout_github_child?: GitCheckoutGitHubChildConfigJSON;
  gitiles_child?: GitilesChildConfigJSON;
  semver_gcs_child?: SemVerGCSChildConfigJSON;
  buildbucket_revision_filter?: BuildbucketRevisionFilterConfigJSON;
}

export interface CopyParentConfig_CopyEntry {
  srcRelPath: string;
  dstRelPath: string;
}

interface CopyParentConfig_CopyEntryJSON {
  src_rel_path?: string;
  dst_rel_path?: string;
}

export interface CopyParentConfig {
  gitiles?: GitilesParentConfig;
  copies?: CopyParentConfig_CopyEntry[];
}

interface CopyParentConfigJSON {
  gitiles?: GitilesParentConfigJSON;
  copies?: CopyParentConfig_CopyEntryJSON[];
}

export interface DEPSLocalGitHubParentConfig {
  depsLocal?: DEPSLocalParentConfig;
  github?: GitHubConfig;
  forkRepoUrl: string;
}

interface DEPSLocalGitHubParentConfigJSON {
  deps_local?: DEPSLocalParentConfigJSON;
  github?: GitHubConfigJSON;
  fork_repo_url?: string;
}

export interface DEPSLocalGerritParentConfig {
  depsLocal?: DEPSLocalParentConfig;
  gerrit?: GerritConfig;
}

interface DEPSLocalGerritParentConfigJSON {
  deps_local?: DEPSLocalParentConfigJSON;
  gerrit?: GerritConfigJSON;
}

export interface GitCheckoutGitHubParentConfig {
  gitCheckout?: GitCheckoutParentConfig;
  forkRepoUrl: string;
}

interface GitCheckoutGitHubParentConfigJSON {
  git_checkout?: GitCheckoutParentConfigJSON;
  fork_repo_url?: string;
}

export interface GitCheckoutGitHubFileParentConfig {
  gitCheckout?: GitCheckoutGitHubParentConfig;
  github?: GitHubConfig;
  preUploadSteps?: PreUploadStep[];
}

interface GitCheckoutGitHubFileParentConfigJSON {
  git_checkout?: GitCheckoutGitHubParentConfigJSON;
  github?: GitHubConfigJSON;
  pre_upload_steps?: string[];
}

export interface GitilesParentConfig {
  gitiles?: GitilesConfig;
  dep?: DependencyConfig;
  gerrit?: GerritConfig;
}

interface GitilesParentConfigJSON {
  gitiles?: GitilesConfigJSON;
  dep?: DependencyConfigJSON;
  gerrit?: GerritConfigJSON;
}

export interface GitilesConfig {
  branch: string;
  repoUrl: string;
  dependencies?: VersionFileConfig[];
}

interface GitilesConfigJSON {
  branch?: string;
  repo_url?: string;
  dependencies?: VersionFileConfigJSON[];
}

export interface DEPSLocalParentConfig {
  gitCheckout?: GitCheckoutParentConfig;
  childPath: string;
  childSubdir: string;
  checkoutPath: string;
  gclientSpec: string;
  preUploadSteps?: PreUploadStep[];
  runHooks: boolean;
}

interface DEPSLocalParentConfigJSON {
  git_checkout?: GitCheckoutParentConfigJSON;
  child_path?: string;
  child_subdir?: string;
  checkout_path?: string;
  gclient_spec?: string;
  pre_upload_steps?: string[];
  run_hooks?: boolean;
}

export interface GitCheckoutParentConfig {
  gitCheckout?: GitCheckoutConfig;
  dep?: DependencyConfig;
}

interface GitCheckoutParentConfigJSON {
  git_checkout?: GitCheckoutConfigJSON;
  dep?: DependencyConfigJSON;
}

export interface FreeTypeParentConfig {
  gitiles?: GitilesParentConfig;
}

interface FreeTypeParentConfigJSON {
  gitiles?: GitilesParentConfigJSON;
}

export interface CIPDChildConfig {
  name: string;
  tag: string;
}

interface CIPDChildConfigJSON {
  name?: string;
  tag?: string;
}

export interface FuchsiaSDKChildConfig {
  includeMacSdk: boolean;
}

interface FuchsiaSDKChildConfigJSON {
  include_mac_sdk?: boolean;
}

export interface SemVerGCSChildConfig {
  gcs?: GCSChildConfig;
  shortRevRegex: string;
  versionRegex: string;
}

interface SemVerGCSChildConfigJSON {
  gcs?: GCSChildConfigJSON;
  short_rev_regex?: string;
  version_regex?: string;
}

export interface GCSChildConfig {
  gcsBucket: string;
  gcsPath: string;
}

interface GCSChildConfigJSON {
  gcs_bucket?: string;
  gcs_path?: string;
}

export interface GitCheckoutChildConfig {
  gitCheckout?: GitCheckoutConfig;
}

interface GitCheckoutChildConfigJSON {
  git_checkout?: GitCheckoutConfigJSON;
}

export interface GitCheckoutGitHubChildConfig {
  gitCheckout?: GitCheckoutChildConfig;
  repoOwner: string;
  repoName: string;
}

interface GitCheckoutGitHubChildConfigJSON {
  git_checkout?: GitCheckoutChildConfigJSON;
  repo_owner?: string;
  repo_name?: string;
}

export interface GitilesChildConfig {
  gitiles?: GitilesConfig;
  path: string;
}

interface GitilesChildConfigJSON {
  gitiles?: GitilesConfigJSON;
  path?: string;
}

export interface NotifierConfig {
  logLevel: NotifierConfig_LogLevel;
  msgType?: NotifierConfig_MsgType[];
  email?: EmailNotifierConfig;
  chat?: ChatNotifierConfig;
  monorail?: MonorailNotifierConfig;
  pubsub?: PubSubNotifierConfig;
  subject: string;
}

interface NotifierConfigJSON {
  log_level?: string;
  msg_type?: string[];
  email?: EmailNotifierConfigJSON;
  chat?: ChatNotifierConfigJSON;
  monorail?: MonorailNotifierConfigJSON;
  pubsub?: PubSubNotifierConfigJSON;
  subject?: string;
}

export interface EmailNotifierConfig {
  emails?: string[];
}

interface EmailNotifierConfigJSON {
  emails?: string[];
}

export interface ChatNotifierConfig {
  roomId: string;
}

interface ChatNotifierConfigJSON {
  room_id?: string;
}

export interface MonorailNotifierConfig {
  project: string;
  owner: string;
  cc?: string[];
  components?: string[];
  labels?: string[];
}

interface MonorailNotifierConfigJSON {
  project?: string;
  owner?: string;
  cc?: string[];
  components?: string[];
  labels?: string[];
}

export interface PubSubNotifierConfig {
  topic: string;
}

interface PubSubNotifierConfigJSON {
  topic?: string;
}

export interface ThrottleConfig {
  attemptCount: number;
  timeWindow?: google_protobuf_Duration;
}

interface ThrottleConfigJSON {
  attempt_count?: number;
  time_window?: google_protobuf_DurationJSON;
}

export interface TransitiveDepConfig {
  child?: VersionFileConfig;
  parent?: VersionFileConfig;
}

interface TransitiveDepConfigJSON {
  child?: VersionFileConfigJSON;
  parent?: VersionFileConfigJSON;
}

export interface VersionFileConfig {
  id: string;
  path: string;
}

interface VersionFileConfigJSON {
  id?: string;
  path?: string;
}

export interface DependencyConfig {
  primary?: VersionFileConfig;
  transitive?: TransitiveDepConfig[];
}

interface DependencyConfigJSON {
  primary?: VersionFileConfigJSON;
  transitive?: TransitiveDepConfigJSON[];
}

export interface GitCheckoutConfig {
  branch: string;
  repoUrl: string;
  revLinkTmpl: string;
  dependencies?: VersionFileConfig[];
}

interface GitCheckoutConfigJSON {
  branch?: string;
  repo_url?: string;
  rev_link_tmpl?: string;
  dependencies?: VersionFileConfigJSON[];
}

export interface BuildbucketRevisionFilterConfig {
  project: string;
  bucket: string;
}

interface BuildbucketRevisionFilterConfigJSON {
  project?: string;
  bucket?: string;
}
