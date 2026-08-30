import React from "react";
import { Domain, SubCategory } from "../types";

interface SubCategoryPillProps {
  domain: Domain;
  subCategory: { key: string; subCat: SubCategory };
  onSelect: (domain: Domain, key: string) => void;
}

export const SubCategoryPill: React.FC<SubCategoryPillProps> = ({
  domain,
  subCategory,
  onSelect,
}) => {
  return (
    <button
      className="sub-category-pill"
      onClick={() => onSelect(domain, subCategory.key)}
    >
      <span className="sub-category-pill-icon">
        {subCategory.subCat.icon || domain.icon}
      </span>
      <span className="sub-category-pill-name">
        {subCategory.subCat.display_name || subCategory.key}
      </span>
    </button>
  );
};

interface SubCategoryPillsProps {
  domain: Domain;
  subCategories: Record<string, SubCategory>;
  onBack: () => void;
  onSelect: (domain: Domain, key: string) => void;
}

export const SubCategoryPills: React.FC<SubCategoryPillsProps> = ({
  domain,
  subCategories,
  onBack,
  onSelect,
}) => {
  return (
    <div className="sub-category-pills">
      <div className="sub-category-pills-header">
        <button className="sub-category-pills-back" onClick={onBack}>
          ← {domain.display_name}
        </button>
      </div>
      <div className="sub-category-pills-list">
        {Object.entries(subCategories).map(([key, subCat]) => (
          <SubCategoryPill
            key={key}
            domain={domain}
            subCategory={{ key, subCat }}
            onSelect={onSelect}
          />
        ))}
      </div>
    </div>
  );
};
